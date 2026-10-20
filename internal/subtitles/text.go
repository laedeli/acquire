package subtitles

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// ErrNotText is a file that is no text in any encoding tried.
var ErrNotText = errors.New("the subtitle file is no text acquire can read")

// UTF8 is a subtitle file's text as UTF-8, which the catalog takes: as it is
// when it is UTF-8 already (its byte order mark left for the catalog to
// drop), else read as UTF-16 when it starts with that byte order mark, else
// in the encoding its language was most often written in before UTF-8 —
// GB18030 then Big5 for Chinese (Big5 first for Traditional), Windows-1250
// for the languages of central Europe, Windows-1251 for Cyrillic ones,
// Windows-1252 for the rest. A file with NUL bytes that is not UTF-16 is no
// text.
func UTF8(b []byte, code string) ([]byte, error) {
	switch {
	case bytes.HasPrefix(b, []byte{0xff, 0xfe}), bytes.HasPrefix(b, []byte{0xfe, 0xff}):
		out, err := decode(unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder(), b)
		if err != nil {
			return nil, ErrNotText
		}
		return out, nil
	case bytes.IndexByte(b, 0) >= 0:
		return nil, ErrNotText
	case utf8.Valid(b):
		return b, nil
	}
	for _, enc := range legacyEncodings(code) {
		if out, err := decode(enc.NewDecoder(), b); err == nil && utf8.Valid(out) && !bytes.ContainsRune(out, utf8.RuneError) {
			return out, nil
		}
	}
	return nil, ErrNotText
}

func decode(d *encoding.Decoder, b []byte) ([]byte, error) { return d.Bytes(b) }

// legacyEncodings are the encodings a file of the source's language code is
// tried in, the likeliest first.
func legacyEncodings(code string) []encoding.Encoding {
	c := strings.ToLower(code)
	switch Primary(c) {
	case "zh":
		if Script(c, "") == "hant" {
			return []encoding.Encoding{traditionalchinese.Big5, simplifiedchinese.GB18030}
		}
		return []encoding.Encoding{simplifiedchinese.GB18030, traditionalchinese.Big5}
	case "pl", "cs", "sk", "hu", "sl", "hr", "ro", "bs", "sr":
		return []encoding.Encoding{charmap.Windows1250, charmap.Windows1252}
	case "ru", "uk", "bg", "mk", "be":
		return []encoding.Encoding{charmap.Windows1251}
	case "el":
		return []encoding.Encoding{charmap.Windows1253}
	case "tr":
		return []encoding.Encoding{charmap.Windows1254}
	case "he":
		return []encoding.Encoding{charmap.Windows1255}
	case "ar":
		return []encoding.Encoding{charmap.Windows1256}
	}
	return []encoding.Encoding{charmap.Windows1252}
}
