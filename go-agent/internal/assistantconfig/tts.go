package assistantconfig

const DefaultTTSModel = "mimo-v2.5-tts"
const VoiceCloneTTSModel = "mimo-v2.5-tts-voiceclone"
const DefaultTTSVoice = "冰糖"

func ValidTTSModel(model string) bool { return model == DefaultTTSModel || model == VoiceCloneTTSModel }
func ValidTTSVoice(voice string) bool {
	switch voice {
	case "mimo_default", "冰糖", "茉莉", "苏打", "白桦", "Mia", "Chloe", "Milo", "Dean":
		return true
	}
	return false
}
