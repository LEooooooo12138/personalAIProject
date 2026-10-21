package core

// MessageRequiresLocalSpeech identifies sensitive provenance independently of
// the selected speech provider. Ordinary local chat can still opt into cloud
// speech; device, knowledge and unclassified legacy content cannot.
func MessageRequiresLocalSpeech(msg Message) bool {
	if msg.EventID != "" || msg.DeviceResult != nil || len(msg.Attachments) > 0 {
		return true
	}
	switch msg.AssistantMode {
	case "local", "cloud", "hybrid":
		return false
	default:
		return true
	}
}
