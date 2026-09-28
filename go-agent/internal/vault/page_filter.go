package vault

import "strings"

// IsInternalPage enforces the metadata visibility rule in the RAG spec.
func IsInternalPage(page *Page) bool {
	for _, tag := range page.Tags {
		if tag == "visibility/internal" || tag == "internal" {
			return true
		}
	}
	if strings.EqualFold(page.Category, "concept") || strings.EqualFold(page.Category, "concepts") {
		for _, tag := range page.Tags {
			switch tag {
			case "rag", "user-facing", "knowledge-base", "game":
				return false
			}
		}
		return true
	}
	return false
}
