package surfaces

import (
	"encoding/json"
	"strings"
)

func timelineValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		if strings.HasPrefix(strings.ToLower(text), "data:image/") && strings.Contains(strings.ToLower(text), ";base64,") {
			return "[inline image data omitted]"
		}
		return text
	}
	data, _ := json.MarshalIndent(redactInlineImageData(value, false), "", "  ")
	return string(data)
}

func redactInlineImageData(value any, imageContext bool) any {
	switch typed := value.(type) {
	case map[string]any:
		isImage := imageContext || strings.Contains(strings.ToLower(str(typed, "type")), "image")
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			lower := strings.ToLower(key)
			if isImage && (lower == "data" || lower == "bytes" || lower == "base64") {
				out[key] = "[inline image data omitted]"
				continue
			}
			out[key] = redactInlineImageData(child, isImage)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = redactInlineImageData(child, imageContext)
		}
		return out
	case string:
		if strings.HasPrefix(strings.ToLower(typed), "data:image/") && strings.Contains(strings.ToLower(typed), ";base64,") {
			return "[inline image data omitted]"
		}
	}
	return value
}

func toolResultImageContent(value any) []map[string]any {
	content, _ := value.([]any)
	images := make([]map[string]any, 0)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if str(block, "type") == "image" {
			images = append(images, block)
		}
	}
	return images
}

func timelineToolResultText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if content, ok := value.([]any); ok {
		var textParts []string
		textOnly := true
		for _, entry := range content {
			block, isBlock := entry.(map[string]any)
			if isBlock && str(block, "type") == "image" {
				continue
			}
			if isBlock && str(block, "type") == "text" && str(block, "text") != "" {
				textParts = append(textParts, str(block, "text"))
				continue
			}
			textOnly = false
		}
		if textOnly && len(textParts) > 0 {
			return strings.Join(textParts, "\n\n")
		}
	}
	clean := sanitizeToolResultValue(value)
	if remaining, ok := clean.([]any); clean == nil || ok && len(remaining) == 0 {
		return ""
	}
	return timelineValue(clean)
}

func sanitizeToolResultValue(value any) any {
	switch value := value.(type) {
	case []any:
		clean := make([]any, 0, len(value))
		for _, entry := range value {
			if block, ok := entry.(map[string]any); ok && str(block, "type") == "image" {
				continue
			}
			if sanitized := sanitizeToolResultValue(entry); sanitized != nil {
				clean = append(clean, sanitized)
			}
		}
		return clean
	case map[string]any:
		if str(value, "type") == "image" {
			return nil
		}
		clean := make(map[string]any, len(value))
		for key, entry := range value {
			if key == "data" && strings.Contains(strings.ToLower(str(value, "media_type")), "image/") {
				continue
			}
			if sanitized := sanitizeToolResultValue(entry); sanitized != nil {
				clean[key] = sanitized
			}
		}
		return clean
	default:
		return value
	}
}
