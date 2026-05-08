package tts

type Voice struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Locale string `json:"locale"`
	Gender string `json:"gender"`
}

// Voices is a curated subset of Edge TTS voices that work well for
// long-form reading. The full list has hundreds of voices; this is a
// pragmatic default that covers the common cases without overwhelming
// the dropdown.
var Voices = []Voice{
	{"en-US-AriaNeural", "Aria (US English)", "en-US", "Female"},
	{"en-US-JennyNeural", "Jenny (US English)", "en-US", "Female"},
	{"en-US-GuyNeural", "Guy (US English)", "en-US", "Male"},
	{"en-US-AndrewNeural", "Andrew (US English)", "en-US", "Male"},
	{"en-US-EmmaNeural", "Emma (US English)", "en-US", "Female"},
	{"en-GB-SoniaNeural", "Sonia (UK English)", "en-GB", "Female"},
	{"en-GB-RyanNeural", "Ryan (UK English)", "en-GB", "Male"},
	{"pt-BR-FranciscaNeural", "Francisca (Português BR)", "pt-BR", "Female"},
	{"pt-BR-AntonioNeural", "Antônio (Português BR)", "pt-BR", "Male"},
	{"pt-BR-ThalitaNeural", "Thalita (Português BR)", "pt-BR", "Female"},
	{"pt-PT-RaquelNeural", "Raquel (Português PT)", "pt-PT", "Female"},
	{"es-ES-ElviraNeural", "Elvira (Español ES)", "es-ES", "Female"},
	{"es-MX-DaliaNeural", "Dalia (Español MX)", "es-MX", "Female"},
	{"fr-FR-DeniseNeural", "Denise (Français)", "fr-FR", "Female"},
	{"de-DE-KatjaNeural", "Katja (Deutsch)", "de-DE", "Female"},
	{"it-IT-ElsaNeural", "Elsa (Italiano)", "it-IT", "Female"},
	{"ja-JP-NanamiNeural", "Nanami (日本語)", "ja-JP", "Female"},
	{"ko-KR-SunHiNeural", "Sun-Hi (한국어)", "ko-KR", "Female"},
	{"zh-CN-XiaoxiaoNeural", "Xiaoxiao (中文)", "zh-CN", "Female"},
}
