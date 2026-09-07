package configyaml

import (
	"strings"
	"unicode"
)

func ProxyGroupOrder(raw string) []string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	header, baseIndent := -1, 0
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		colon := strings.Index(trimmed, ":")
		if colon < 0 || strings.Trim(trimmed[:colon], "'\"") != "proxy-groups" {
			continue
		}
		remainder := strings.TrimSpace(trimmed[colon+1:])
		if remainder != "" && !strings.HasPrefix(remainder, "#") {
			return nil
		}
		header, baseIndent = index, indentWidth(line)
		break
	}
	if header < 0 {
		return nil
	}
	order, seen := []string{}, map[string]bool{}
	for _, line := range lines[header+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if indentWidth(line) <= baseIndent {
			break
		}
		if !strings.HasPrefix(trimmed, "-") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		flow := strings.HasPrefix(value, "{")
		value = strings.TrimSpace(strings.TrimPrefix(value, "{"))
		if !strings.HasPrefix(value, "name:") {
			continue
		}
		name := yamlScalar(strings.TrimSpace(strings.TrimPrefix(value, "name:")), flow)
		if name != "" && !seen[name] {
			seen[name] = true
			order = append(order, name)
		}
	}
	return order
}

func indentWidth(line string) int {
	width := 0
	for _, char := range line {
		if char == ' ' {
			width++
		} else if char == '\t' {
			width += 2
		} else {
			break
		}
	}
	return width
}

func yamlScalar(value string, flow bool) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if value[0] == '\'' || value[0] == '"' {
		quote := rune(value[0])
		var output strings.Builder
		runes := []rune(value)
		for index := 1; index < len(runes); index++ {
			char := runes[index]
			if quote == '\'' && char == '\'' && index+1 < len(runes) && runes[index+1] == '\'' {
				output.WriteRune('\'')
				index++
				continue
			}
			if char == quote {
				return output.String()
			}
			if quote == '"' && char == '\\' && index+1 < len(runes) {
				index++
				escaped := runes[index]
				switch escaped {
				case 'n':
					output.WriteRune('\n')
				case 'r':
					output.WriteRune('\r')
				case 't':
					output.WriteRune('\t')
				default:
					output.WriteRune(escaped)
				}
			} else {
				output.WriteRune(char)
			}
		}
		return strings.TrimSpace(output.String())
	}
	if flow {
		if end := strings.IndexAny(value, ",}"); end >= 0 {
			value = value[:end]
		}
	}
	if comment := strings.Index(value, " #"); comment >= 0 {
		value = value[:comment]
	}
	return strings.TrimRightFunc(value, unicode.IsSpace)
}
