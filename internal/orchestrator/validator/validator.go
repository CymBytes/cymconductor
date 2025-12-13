// Package validator provides DSL scenario validation.
package validator

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"cymbytes.com/cymconductor/pkg/dsl"
	"github.com/go-playground/validator/v10"
)

// ValidationError represents a single validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// ValidationResult holds the result of validation.
type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

// Validator validates DSL scenarios.
type Validator struct {
	validate *validator.Validate
}

// New creates a new validator.
func New() *Validator {
	v := validator.New()

	// Register custom validators
	v.RegisterValidation("safe_path", validateSafePath)
	v.RegisterValidation("safe_url", validateSafeURL)

	return &Validator{
		validate: v,
	}
}

// ValidateScenario validates a complete scenario.
func (v *Validator) ValidateScenario(scenario *dsl.Scenario) *ValidationResult {
	result := &ValidationResult{Valid: true}

	// 1. Validate schema version
	if scenario.Schema != dsl.SchemaVersion {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "$schema",
			Rule:    "schema_version",
			Message: fmt.Sprintf("Invalid schema version: expected %s, got %s", dsl.SchemaVersion, scenario.Schema),
		})
	}

	// 2. Validate basic struct fields
	if err := v.validate.Struct(scenario); err != nil {
		result.Valid = false
		if validationErrors, ok := err.(validator.ValidationErrors); ok {
			for _, e := range validationErrors {
				result.Errors = append(result.Errors, ValidationError{
					Field:   e.Field(),
					Rule:    e.Tag(),
					Message: formatValidationError(e),
				})
			}
		}
	}

	// 3. Validate step order is sequential
	if err := validateStepOrder(scenario.Steps); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, *err)
	}

	// 4. Validate each step's action type and parameters
	for i, step := range scenario.Steps {
		stepErrors := v.validateStep(&step, i)
		if len(stepErrors) > 0 {
			result.Valid = false
			result.Errors = append(result.Errors, stepErrors...)
		}
	}

	// 5. Check for duplicate step IDs
	if err := validateUniqueStepIDs(scenario.Steps); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, *err)
	}

	return result
}

// validateStep validates a single step.
func (v *Validator) validateStep(step *dsl.Step, index int) []ValidationError {
	var errors []ValidationError
	prefix := fmt.Sprintf("steps[%d]", index)

	// Check action type is allowed
	if !dsl.IsValidAction(step.ActionType) {
		errors = append(errors, ValidationError{
			Field:   prefix + ".action_type",
			Rule:    "allowed_action",
			Message: fmt.Sprintf("Action type '%s' is not allowed. Allowed types: %v", step.ActionType, dsl.AllowedActions),
		})
		return errors // Can't validate parameters for unknown action type
	}

	// Validate target
	if len(step.Target.Labels) == 0 {
		errors = append(errors, ValidationError{
			Field:   prefix + ".target.labels",
			Rule:    "required",
			Message: "Target labels are required",
		})
	}

	// Validate target.count
	if step.Target.Count != "all" && step.Target.Count != "any" {
		var count int
		if _, err := fmt.Sscanf(step.Target.Count, "%d", &count); err != nil || count < 1 {
			errors = append(errors, ValidationError{
				Field:   prefix + ".target.count",
				Rule:    "valid_count",
				Message: "Target count must be 'all', 'any', or a positive integer",
			})
		}
	}

	// Parse and validate parameters based on action type
	params, err := step.ParseParameters()
	if err != nil {
		errors = append(errors, ValidationError{
			Field:   prefix + ".parameters",
			Rule:    "json_parse",
			Message: fmt.Sprintf("Failed to parse parameters: %v", err),
		})
		return errors
	}

	// Validate parameters struct
	if err := v.validate.Struct(params); err != nil {
		if validationErrors, ok := err.(validator.ValidationErrors); ok {
			for _, e := range validationErrors {
				errors = append(errors, ValidationError{
					Field:   prefix + ".parameters." + e.Field(),
					Rule:    e.Tag(),
					Message: formatValidationError(e),
				})
			}
		}
	}

	// Additional security validations based on action type
	securityErrors := v.validateSecurityConstraints(step.ActionType, params, prefix)
	errors = append(errors, securityErrors...)

	return errors
}

// validateSecurityConstraints performs security-specific validations.
func (v *Validator) validateSecurityConstraints(actionType dsl.ActionType, params interface{}, prefix string) []ValidationError {
	var errors []ValidationError

	switch actionType {
	case dsl.ActionSimulateBrowsing:
		p := params.(*dsl.SimulateBrowsingParams)
		for i, urlStr := range p.URLs {
			if err := validateURLSecurity(urlStr); err != nil {
				errors = append(errors, ValidationError{
					Field:   fmt.Sprintf("%s.parameters.urls[%d]", prefix, i),
					Rule:    "secure_url",
					Message: err.Error(),
				})
			}
		}

	case dsl.ActionSimulateFileActivity:
		p := params.(*dsl.SimulateFileActivityParams)
		if err := validatePathSecurity(p.TargetDirectory); err != nil {
			errors = append(errors, ValidationError{
				Field:   prefix + ".parameters.target_directory",
				Rule:    "secure_path",
				Message: err.Error(),
			})
		}

	case dsl.ActionSimulateProcessActivity:
		p := params.(*dsl.SimulateProcessActivityParams)
		// All processes must be from approved list (validated at execution time by agent)
		// But we can still flag obviously dangerous process names
		for i, proc := range p.AllowedProcesses {
			if containsShellMetachars(proc) {
				errors = append(errors, ValidationError{
					Field:   fmt.Sprintf("%s.parameters.allowed_processes[%d]", prefix, i),
					Rule:    "no_shell_metachar",
					Message: "Process name contains shell metacharacters",
				})
			}
		}

	case dsl.ActionSimulateEmailTraffic:
		p := params.(*dsl.SimulateEmailTrafficParams)
		// Validate server is not a well-known public server (to prevent abuse)
		if isPublicEmailServer(p.Server) {
			errors = append(errors, ValidationError{
				Field:   prefix + ".parameters.server",
				Rule:    "no_public_server",
				Message: "Cannot use public email servers in lab scenarios",
			})
		}
	}

	return errors
}

// ValidateScenarioJSON validates a scenario from raw JSON.
func (v *Validator) ValidateScenarioJSON(jsonData []byte) (*dsl.Scenario, *ValidationResult) {
	var scenario dsl.Scenario

	// First, check JSON is valid
	if err := json.Unmarshal(jsonData, &scenario); err != nil {
		return nil, &ValidationResult{
			Valid: false,
			Errors: []ValidationError{{
				Field:   "",
				Rule:    "json_parse",
				Message: fmt.Sprintf("Invalid JSON: %v", err),
			}},
		}
	}

	// Then validate the scenario
	result := v.ValidateScenario(&scenario)
	if !result.Valid {
		return nil, result
	}

	return &scenario, result
}

// ============================================================
// Helper functions
// ============================================================

func validateStepOrder(steps []dsl.Step) *ValidationError {
	for i, step := range steps {
		expectedOrder := i + 1
		if step.Order != expectedOrder {
			return &ValidationError{
				Field:   fmt.Sprintf("steps[%d].order", i),
				Rule:    "sequential_order",
				Message: fmt.Sprintf("Step order must be sequential: expected %d, got %d", expectedOrder, step.Order),
			}
		}
	}
	return nil
}

func validateUniqueStepIDs(steps []dsl.Step) *ValidationError {
	seen := make(map[string]int)
	for i, step := range steps {
		if prev, ok := seen[step.ID]; ok {
			return &ValidationError{
				Field:   fmt.Sprintf("steps[%d].id", i),
				Rule:    "unique_id",
				Message: fmt.Sprintf("Duplicate step ID: %s (also at step %d)", step.ID, prev),
			}
		}
		seen[step.ID] = i
	}
	return nil
}

func validateURLSecurity(urlStr string) error {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}

	// Allow only http/https
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https")
	}

	// Check for dangerous hosts (file://, localhost except lab networks)
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || host == "127.0.0.1" {
		return fmt.Errorf("localhost URLs are not allowed")
	}

	// Allow internal lab IPs (10.x.x.x)
	if strings.HasPrefix(host, "10.") {
		return nil
	}

	// Block internal IPs that shouldn't be in lab scenarios
	if strings.HasPrefix(host, "192.168.") || strings.HasPrefix(host, "172.") {
		return fmt.Errorf("non-lab internal IPs are not allowed")
	}

	return nil
}

func validatePathSecurity(path string) error {
	// Check for path traversal
	if strings.Contains(path, "..") {
		return fmt.Errorf("path traversal not allowed")
	}

	// Check for shell metacharacters
	if containsShellMetachars(path) {
		return fmt.Errorf("path contains shell metacharacters")
	}

	// Block sensitive paths
	sensitivePaths := []string{
		"/etc", "/var", "/usr", "/bin", "/sbin", "/root",
		"C:\\Windows", "C:\\Program Files",
	}
	lowerPath := strings.ToLower(path)
	for _, sensitive := range sensitivePaths {
		if strings.HasPrefix(lowerPath, strings.ToLower(sensitive)) {
			return fmt.Errorf("access to %s is not allowed", sensitive)
		}
	}

	return nil
}

func containsShellMetachars(s string) bool {
	dangerousChars := regexp.MustCompile(`[;&|$\x60\\<>]`)
	return dangerousChars.MatchString(s)
}

func isPublicEmailServer(server string) bool {
	publicServers := []string{
		"smtp.gmail.com", "smtp.outlook.com", "smtp.mail.yahoo.com",
		"smtp.aol.com", "smtp.zoho.com", "smtp.sendgrid.net",
	}
	server = strings.ToLower(server)
	for _, pub := range publicServers {
		if server == pub {
			return true
		}
	}
	return false
}

func validateSafePath(fl validator.FieldLevel) bool {
	return validatePathSecurity(fl.Field().String()) == nil
}

func validateSafeURL(fl validator.FieldLevel) bool {
	return validateURLSecurity(fl.Field().String()) == nil
}

func formatValidationError(e validator.FieldError) string {
	switch e.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", e.Field())
	case "min":
		return fmt.Sprintf("%s must be at least %s", e.Field(), e.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s", e.Field(), e.Param())
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", e.Field(), e.Param())
	case "url":
		return fmt.Sprintf("%s must be a valid URL", e.Field())
	case "uuid4":
		return fmt.Sprintf("%s must be a valid UUID v4", e.Field())
	default:
		return fmt.Sprintf("%s failed validation: %s", e.Field(), e.Tag())
	}
}
