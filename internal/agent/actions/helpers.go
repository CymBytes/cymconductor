// Package actions provides predefined action implementations for the agent.
package actions

// Helper functions for parsing parameters from map[string]interface{}

// Ensure all helper functions are considered used (some reserved for future use)
var (
	_ = getFloat
)

// getString extracts a string value from params.
func getString(params map[string]interface{}, key string) (string, bool) {
	if v, ok := params[key]; ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return "", false
}

// getInt extracts an integer value from params.
func getInt(params map[string]interface{}, key string) (int, bool) {
	if v, ok := params[key]; ok {
		switch n := v.(type) {
		case int:
			return n, true
		case int64:
			return int(n), true
		case float64:
			return int(n), true
		}
	}
	return 0, false
}

// getBool extracts a boolean value from params.
func getBool(params map[string]interface{}, key string) (bool, bool) {
	if v, ok := params[key]; ok {
		if b, ok := v.(bool); ok {
			return b, true
		}
	}
	return false, false
}

// getStringSlice extracts a string slice from params.
func getStringSlice(params map[string]interface{}, key string) ([]string, bool) {
	if v, ok := params[key]; ok {
		switch s := v.(type) {
		case []string:
			return s, true
		case []interface{}:
			result := make([]string, 0, len(s))
			for _, item := range s {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
			return result, len(result) > 0
		}
	}
	return nil, false
}

// getFloat extracts a float64 value from params.
func getFloat(params map[string]interface{}, key string) (float64, bool) {
	if v, ok := params[key]; ok {
		switch n := v.(type) {
		case float64:
			return n, true
		case int:
			return float64(n), true
		case int64:
			return float64(n), true
		}
	}
	return 0, false
}
