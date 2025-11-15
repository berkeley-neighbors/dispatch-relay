package utils

// ReplaceTemplateVars replaces template variables in format {{varname}} with actual values
func ReplaceTemplateVars(template string, vars map[string]string) string {
	result := template

	for key, value := range vars {
		placeholder := "{{" + key + "}}"
		result = ReplaceString(result, placeholder, value)
	}

	return result
}
