package indexer

import (
	"strconv"
	"strings"
)

// Caps is what a source says it supports, from t=caps. It is stored as JSON
// with the source and shown in the console, so the tags are part of the API.
//
// The claims are a HINT, not a contract. Measured against a real fleet: fewer
// than half of the sources advertise tv search params, only a handful
// advertise tvdbid, and one of those returns zero items for every id query it
// is given. The engine uses caps to decide where to ask FIRST and always keeps
// a broader stage behind it.
type Caps struct {
	Server      CapsServer `json:"server"`
	Limits      CapsLimits `json:"limits"`
	Search      SearchMode `json:"search"`
	TVSearch    SearchMode `json:"tvSearch"`
	MovieSearch SearchMode `json:"movieSearch"`
	Categories  []Category `json:"categories"`
}

// CapsServer names the software answering.
type CapsServer struct {
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// CapsLimits are the page sizes a source allows (not its daily allowance).
type CapsLimits struct {
	Max     int `json:"max,omitempty"`
	Default int `json:"default,omitempty"`
}

// SearchMode is one t= function and the parameters it accepts.
type SearchMode struct {
	Available bool     `json:"available"`
	Params    []string `json:"params"`
}

// Category is a newznab category and its subcategories.
type Category struct {
	ID      int        `json:"id"`
	Name    string     `json:"name"`
	Subcats []Category `json:"subcats,omitempty"`
}

type capsXML struct {
	Server struct {
		Title   string `xml:"title,attr"`
		Version string `xml:"version,attr"`
	} `xml:"server"`
	Limits struct {
		Max     string `xml:"max,attr"`
		Default string `xml:"default,attr"`
	} `xml:"limits"`
	Searching struct {
		Search      modeXML `xml:"search"`
		TVSearch    modeXML `xml:"tv-search"`
		MovieSearch modeXML `xml:"movie-search"`
	} `xml:"searching"`
	Categories struct {
		Items []categoryXML `xml:"category"`
	} `xml:"categories"`
}

type modeXML struct {
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

type categoryXML struct {
	ID      string        `xml:"id,attr"`
	Name    string        `xml:"name,attr"`
	Subcats []categoryXML `xml:"subcat"`
}

// ParseCaps reads a t=caps document.
func ParseCaps(body []byte) (*Caps, error) {
	var x capsXML
	dec := newDecoder(body)
	root, err := rootElement(dec)
	if err != nil {
		return nil, err
	}
	if root.Name.Local != "caps" {
		return nil, &Error{Kind: KindOther, Msg: "not a caps document"}
	}
	if err := dec.DecodeElement(&x, &root); err != nil {
		return nil, err
	}
	c := &Caps{
		Server:      CapsServer{Title: clean(x.Server.Title, 120), Version: clean(x.Server.Version, 40)},
		Search:      mode(x.Searching.Search),
		TVSearch:    mode(x.Searching.TVSearch),
		MovieSearch: mode(x.Searching.MovieSearch),
		Categories:  categories(x.Categories.Items),
	}
	c.Limits.Max, _ = strconv.Atoi(strings.TrimSpace(x.Limits.Max))
	c.Limits.Default, _ = strconv.Atoi(strings.TrimSpace(x.Limits.Default))
	return c, nil
}

func mode(m modeXML) SearchMode {
	out := SearchMode{Available: strings.EqualFold(strings.TrimSpace(m.Available), "yes"), Params: []string{}}
	for _, p := range strings.Split(m.SupportedParams, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out.Params = append(out.Params, p)
		}
	}
	if out.Available && len(out.Params) == 0 {
		// Available without a list: the protocol's minimum is a text query.
		out.Params = []string{"q"}
	}
	return out
}

func categories(in []categoryXML) []Category {
	out := []Category{}
	for _, c := range in {
		id, err := strconv.Atoi(strings.TrimSpace(c.ID))
		if err != nil || id <= 0 {
			continue
		}
		cat := Category{ID: id, Name: clean(c.Name, 80)}
		if subs := categories(c.Subcats); len(subs) > 0 {
			cat.Subcats = subs
		}
		out = append(out, cat)
	}
	return out
}

func (m SearchMode) accepts(param string) bool {
	if !m.Available {
		return false
	}
	for _, p := range m.Params {
		if p == param {
			return true
		}
	}
	return false
}

// AcceptsTVDBID: a tvsearch by tvdbid is worth sending.
func (c *Caps) AcceptsTVDBID() bool { return c != nil && c.TVSearch.accepts("tvdbid") }

// AcceptsMovieIMDBID: a movie search by imdbid is worth sending.
func (c *Caps) AcceptsMovieIMDBID() bool { return c != nil && c.MovieSearch.accepts("imdbid") }

// AcceptsSeasonEp requires BOTH season and ep; advertising only one is not
// enough to build a coordinate query from.
func (c *Caps) AcceptsSeasonEp() bool {
	return c != nil && c.TVSearch.accepts("season") && c.TVSearch.accepts("ep")
}
