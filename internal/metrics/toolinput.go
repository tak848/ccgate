package metrics

const maxToolInputFieldLen = 2000

// CapToolInput applies a length cap (rune-wise truncate) to each field of
// ToolInputFields destined for JSONL persistence. It does NOT normalize
// whitespace: the original command text is preserved verbatim so the entry
// can be read back without being mangled.
func CapToolInput(tif ToolInputFields) ToolInputFields {
	tif.Command = truncateRunes(tif.Command, maxToolInputFieldLen)
	tif.FilePath = truncateRunes(tif.FilePath, maxToolInputFieldLen)
	tif.Path = truncateRunes(tif.Path, maxToolInputFieldLen)
	tif.Pattern = truncateRunes(tif.Pattern, maxToolInputFieldLen)
	return tif
}

func truncateRunes(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return s
}
