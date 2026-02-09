package monitor

// FirstNonEmpty returns the first non-empty string from the input sequence.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// MapStringValue safely extracts a string value by key from a generic map.
func MapStringValue(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return s
}
