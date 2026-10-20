// Package subtitles is what acquire decides about a title's subtitles: which
// of the instance's languages a title has, what to ask a subtitle source for,
// which of its results to take, and the text of the one taken.
package subtitles

import (
	"regexp"
	"strings"
)

// Primary is the language a code names, as one key to compare by: its ISO
// 639-1 code where it has one ("ger", "deu", "de" and "de-CH" are "de"),
// else the code's primary subtag in lower case; "" for a code that names no
// language (und, zxx, mul, mis, empty).
func Primary(code string) string {
	p := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(code, "_", "-")))
	if i := strings.IndexByte(p, '-'); i >= 0 {
		p = p[:i]
	}
	switch p {
	case "", "und", "zxx", "mul", "mis":
		return ""
	case "ze": // the source's bilingual Chinese, of no script it says
		return "zh"
	}
	if one, ok := iso6392To1[p]; ok {
		return one
	}
	return p
}

// Script is the Chinese script a code or a track's label names: "hant" for
// Traditional (zh-Hant, zh-TW, zh-HK, zh-MO; a label saying traditional, 繁,
// CHT or BIG5), "hans" for Simplified (zh-Hans, zh-CN, zh-SG; simplified, 简,
// CHS, GB), "" when neither says.
func Script(code, label string) string {
	c := strings.ToLower(strings.ReplaceAll(code, "_", "-"))
	switch {
	case strings.Contains(c, "hant"), strings.HasSuffix(c, "-tw"), strings.HasSuffix(c, "-hk"), strings.HasSuffix(c, "-mo"):
		return "hant"
	case strings.Contains(c, "hans"), strings.HasSuffix(c, "-cn"), strings.HasSuffix(c, "-sg"):
		return "hans"
	}
	l := strings.ToLower(label)
	switch {
	case traditionalRE.MatchString(l):
		return "hant"
	case simplifiedRE.MatchString(l):
		return "hans"
	}
	return ""
}

var (
	traditionalRE = regexp.MustCompile(`traditional|繁|\bcht\b|\bbig5\b|\btc\b`)
	simplifiedRE  = regexp.MustCompile(`simplified|简|\bchs\b|\bgb\b|\bsc\b`)
)

// Name is a language as people say it, for the console's detail lines and
// logs; the code itself for one it does not know. Chinese is Chinese, and
// names its script when the code does.
func Name(tag string) string {
	p := Primary(tag)
	if p == "zh" {
		switch Script(tag, "") {
		case "hant":
			return "Chinese (Traditional)"
		case "hans":
			return "Chinese (Simplified)"
		default:
			return "Chinese"
		}
	}
	if n, ok := names[p]; ok {
		return n
	}
	return tag
}

var names = map[string]string{
	"en": "English", "de": "German", "fr": "French", "es": "Spanish", "it": "Italian", "pt": "Portuguese",
	"nl": "Dutch", "sv": "Swedish", "da": "Danish", "no": "Norwegian", "nb": "Norwegian", "fi": "Finnish",
	"pl": "Polish", "cs": "Czech", "hu": "Hungarian", "ro": "Romanian", "el": "Greek", "tr": "Turkish",
	"ru": "Russian", "uk": "Ukrainian", "ja": "Japanese", "ko": "Korean", "ar": "Arabic", "he": "Hebrew",
	"hi": "Hindi", "th": "Thai", "vi": "Vietnamese", "id": "Indonesian", "hr": "Croatian", "sr": "Serbian",
	"sl": "Slovenian", "sk": "Slovak", "bg": "Bulgarian",
}

// SourceCodes are the codes a subtitle source (OpenSubtitles) is asked for a
// language in, the preferred first. Chinese that names a script asks for
// that script alone (zh-Hans, zh-CN: zh-cn; zh-Hant, zh-TW: zh-tw); Chinese
// that names none asks for both, Simplified first. Portuguese is Portugal's,
// then Brazil's, and a tag of Brazil's asks for Brazil's alone.
func SourceCodes(tag string) []string {
	p := Primary(tag)
	region := ""
	if parts := strings.Split(strings.ToLower(strings.ReplaceAll(tag, "_", "-")), "-"); len(parts) > 1 {
		region = parts[len(parts)-1]
	}
	switch p {
	case "":
		return nil
	case "zh":
		switch Script(tag, "") {
		case "hant":
			return []string{"zh-tw"}
		case "hans":
			return []string{"zh-cn"}
		}
		return []string{"zh-cn", "zh-tw"}
	case "pt":
		if region == "br" {
			return []string{"pt-br"}
		}
		return []string{"pt-pt", "pt-br"}
	}
	return []string{p}
}

// TagOf is the language tag a source's code is attached as: zh-cn is
// zh-Hans, zh-tw zh-Hant, pt-br pt-BR, pt-pt pt-PT, and any other code its
// primary language.
func TagOf(code string) string {
	switch c := strings.ToLower(code); c {
	case "zh-cn":
		return "zh-Hans"
	case "zh-tw":
		return "zh-Hant"
	case "pt-br":
		return "pt-BR"
	case "pt-pt":
		return "pt-PT"
	case "ze":
		return "zh" // the source's bilingual Chinese: no script it says
	default:
		return Primary(c)
	}
}

// Serves reports whether a subtitle in code (a track's language, or a
// source's) serves the language want, and how: "" as it is, or "hant" when
// it is in Traditional characters for a Chinese want that names no script
// (zh), which either script serves. A Chinese want that names a script
// (zh-Hans, zh-Hant, zh-CN, zh-TW, …) is served by a subtitle in that script,
// or in a script it doesn't say (zh, the source's ze), and not by one in the
// other script: nothing turns one script into the other, so that want stays
// open (OtherScript says the title has one in the other characters).
func Serves(code, label, want string) (bool, string) {
	if Primary(code) == "" || Primary(code) != Primary(want) {
		return false, ""
	}
	if Primary(want) != "zh" {
		return true, ""
	}
	have, wanted := Script(code, label), Script(want, "")
	switch {
	case wanted == "" && have == "hant":
		return true, "hant"
	case wanted == "", have == "", have == wanted:
		return true, ""
	}
	return false, ""
}

// OtherScript is the script of a Chinese subtitle in code that is in the
// other characters than the want names ("hant" or "hans"): one that doesn't
// serve it. "" for one that does, and for a want that names no script.
func OtherScript(code, label, want string) string {
	if Primary(code) != "zh" || Primary(want) != "zh" {
		return ""
	}
	have, wanted := Script(code, label), Script(want, "")
	if wanted != "" && have != "" && have != wanted {
		return have
	}
	return ""
}

// iso6392To1 maps the ISO 639-2 codes (B and T) of common languages to their
// ISO 639-1 code.
var iso6392To1 = map[string]string{
	"eng": "en", "ger": "de", "deu": "de", "fre": "fr", "fra": "fr", "spa": "es", "ita": "it", "por": "pt",
	"dut": "nl", "nld": "nl", "swe": "sv", "dan": "da", "nor": "no", "nob": "nb", "nno": "nn", "fin": "fi",
	"pol": "pl", "cze": "cs", "ces": "cs", "hun": "hu", "rum": "ro", "ron": "ro", "gre": "el", "ell": "el",
	"tur": "tr", "rus": "ru", "ukr": "uk", "jpn": "ja", "kor": "ko", "chi": "zh", "zho": "zh", "ara": "ar",
	"heb": "he", "hin": "hi", "tha": "th", "vie": "vi", "ind": "id", "hrv": "hr", "srp": "sr", "slv": "sl",
	"slo": "sk", "slk": "sk", "bul": "bg", "ice": "is", "isl": "is", "est": "et", "lav": "lv", "lit": "lt",
	"cat": "ca", "baq": "eu", "eus": "eu", "glg": "gl", "may": "ms", "msa": "ms", "per": "fa", "fas": "fa",
	"bos": "bs", "mac": "mk", "mkd": "mk", "alb": "sq", "sqi": "sq", "wel": "cy", "cym": "cy", "gle": "ga",
	"tam": "ta", "tel": "te", "ben": "bn", "urd": "ur", "fil": "fil", "tgl": "tl",
}
