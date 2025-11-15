package utils

// ParseEnvironmentVariableList parses a comma-separated string into a list of uppercase strings
func ParseEnvironmentVariableList(methods string) []string {
	if methods == "" {
		return []string{}
	}

	var result []string
	for _, method := range SplitAndTrim(methods, ",") {
		normalized := UpperString(method)
		if normalized != "" {
			result = append(result, normalized)
		}
	}
	return result
}

// SplitAndTrim splits a string by separator and trims each part
func SplitAndTrim(s string, sep string) []string {
	var result []string
	for _, part := range SplitString(s, sep) {
		trimmed := TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// SplitString splits a string by separator
func SplitString(s string, sep string) []string {
	if s == "" {
		return []string{}
	}

	var result []string
	current := ""

	for i := 0; i < len(s); i++ {
		if string(s[i]) == sep {
			result = append(result, current)
			current = ""
		} else {
			current += string(s[i])
		}
	}
	result = append(result, current)
	return result
}

// TrimSpace removes leading and trailing whitespace from a string
func TrimSpace(s string) string {
	start := 0
	end := len(s)

	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}

	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}

	return s[start:end]
}

// UpperString converts a string to uppercase
func UpperString(s string) string {
	upper := ""
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			upper += string(c - 32)
		} else {
			upper += string(c)
		}
	}

	return upper
}

// ReplaceString replaces all occurrences of old with new in string s
func ReplaceString(s string, old string, new string) string {
	if s == "" || old == "" {
		return s
	}

	result := ""
	i := 0

	for i < len(s) {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result += new
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}

	return result
}
