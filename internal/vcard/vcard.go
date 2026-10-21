// Package vcard reads and writes vCards (RFC 2426 / 6350 and the vCard 2.1
// files old phones export) without losing anything it does not understand.
//
// A Card is the ordered list of its properties exactly as they came in;
// Contact (contact.go) is the structured view the app edits, and Apply
// writes an edited Contact back into the Card touching only the parts that
// changed, so iOS, Nextcloud and Android properties survive a round trip.
package vcard

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// Param is one property parameter; a parameter may carry several values
// (TYPE=work,voice or TYPE=work;TYPE=voice end up the same).
type Param struct {
	Name   string
	Values []string
}

// Property is one content line: [group.]NAME;params:value. Value is kept
// escaped as on the wire (structured values need the escapes to split).
type Property struct {
	Group  string
	Name   string // upper case
	Params []Param
	Value  string
}

// Card is one vCard.
type Card struct {
	Props []*Property
}

// ErrNoCard says the data held no BEGIN:VCARD … END:VCARD block.
var ErrNoCard = errors.New("no vCard found")

// ---------- parameters ----------

// Param returns the first value of a parameter ("" when absent).
func (p *Property) Param(name string) string {
	for _, pr := range p.Params {
		if strings.EqualFold(pr.Name, name) && len(pr.Values) > 0 {
			return pr.Values[0]
		}
	}
	return ""
}

// Types returns every TYPE value, lower case (TYPE=WORK,VOICE;TYPE=pref → work voice pref).
func (p *Property) Types() []string {
	var out []string
	for _, pr := range p.Params {
		if strings.EqualFold(pr.Name, "TYPE") {
			for _, v := range pr.Values {
				for _, x := range strings.Split(v, ",") {
					if x = strings.ToLower(strings.TrimSpace(x)); x != "" {
						out = append(out, x)
					}
				}
			}
		}
	}
	return out
}

// HasType reports whether TYPE includes t (case-insensitive).
func (p *Property) HasType(t string) bool {
	for _, x := range p.Types() {
		if x == strings.ToLower(t) {
			return true
		}
	}
	return false
}

// SetParam replaces a parameter (no values removes it).
func (p *Property) SetParam(name string, values ...string) {
	out := p.Params[:0]
	for _, pr := range p.Params {
		if !strings.EqualFold(pr.Name, name) {
			out = append(out, pr)
		}
	}
	p.Params = out
	if len(values) > 0 {
		p.Params = append(p.Params, Param{Name: strings.ToUpper(name), Values: values})
	}
}

// ---------- values ----------

// Text is the unescaped value of a text property.
func (p *Property) Text() string { return Unescape(p.Value) }

// SetText stores an unescaped text value.
func (p *Property) SetText(v string) { p.Value = Escape(v) }

// Components splits a structured value (N, ADR, ORG, GENDER) on unescaped
// semicolons and unescapes each part.
func (p *Property) Components() []string {
	parts := splitUnescaped(p.Value, ';')
	for i := range parts {
		parts[i] = Unescape(parts[i])
	}
	return parts
}

// SetComponents stores a structured value.
func (p *Property) SetComponents(parts ...string) {
	esc := make([]string, len(parts))
	for i, x := range parts {
		esc[i] = Escape(x)
	}
	p.Value = strings.Join(esc, ";")
}

// List splits a comma list (CATEGORIES, NICKNAME).
func (p *Property) List() []string {
	var out []string
	for _, x := range splitUnescaped(p.Value, ',') {
		if x = strings.TrimSpace(Unescape(x)); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// SetList stores a comma list.
func (p *Property) SetList(items []string) {
	esc := make([]string, 0, len(items))
	for _, x := range items {
		if x = strings.TrimSpace(x); x != "" {
			esc = append(esc, Escape(x))
		}
	}
	p.Value = strings.Join(esc, ",")
}

// Escape escapes a text value: backslash, comma, semicolon, newline.
func Escape(s string) string {
	if !strings.ContainsAny(s, "\\,;\n\r") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			b.WriteString(`\\`)
		case ',':
			b.WriteString(`\,`)
		case ';':
			b.WriteString(`\;`)
		case '\r':
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			b.WriteString(`\n`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Unescape reverses Escape (and tolerates the sloppy escapes some exporters write).
func Unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 == len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func splitUnescaped(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// ---------- card access ----------

// Get returns the first property with this name (nil when absent).
func (c *Card) Get(name string) *Property {
	for _, p := range c.Props {
		if p.Name == strings.ToUpper(name) {
			return p
		}
	}
	return nil
}

// All returns every property with this name.
func (c *Card) All(name string) []*Property {
	var out []*Property
	for _, p := range c.Props {
		if p.Name == strings.ToUpper(name) {
			out = append(out, p)
		}
	}
	return out
}

// Value is the unescaped text of the first property with this name.
func (c *Card) Value(name string) string {
	if p := c.Get(name); p != nil {
		return p.Text()
	}
	return ""
}

// Set replaces every property with this name by one text property ("" removes them).
func (c *Card) Set(name, value string) {
	name = strings.ToUpper(name)
	if p := c.Get(name); p != nil && value != "" {
		p.SetText(value)
		c.removeWhere(func(q *Property) bool { return q.Name == name && q != p })
		return
	}
	c.Remove(name)
	if value != "" {
		p := &Property{Name: name}
		p.SetText(value)
		c.Add(p)
	}
}

// Add appends a property before END (the order clients like).
func (c *Card) Add(p *Property) { c.Props = append(c.Props, p) }

// Remove drops every property with one of these names.
func (c *Card) Remove(names ...string) {
	set := map[string]bool{}
	for _, n := range names {
		set[strings.ToUpper(n)] = true
	}
	c.removeWhere(func(p *Property) bool { return set[p.Name] })
}

func (c *Card) removeWhere(drop func(*Property) bool) {
	out := c.Props[:0]
	for _, p := range c.Props {
		if !drop(p) {
			out = append(out, p)
		}
	}
	for i := len(out); i < len(c.Props); i++ {
		c.Props[i] = nil
	}
	c.Props = out
}

// GroupProps returns the other properties sharing p's group (item1.X-ABLabel for item1.TEL).
func (c *Card) GroupProps(p *Property) []*Property {
	if p.Group == "" {
		return nil
	}
	var out []*Property
	for _, q := range c.Props {
		if q != p && strings.EqualFold(q.Group, p.Group) {
			out = append(out, q)
		}
	}
	return out
}

// FreeGroup returns an itemN group name not used yet.
func (c *Card) FreeGroup() string {
	used := map[string]bool{}
	for _, p := range c.Props {
		used[strings.ToLower(p.Group)] = true
	}
	for i := 1; ; i++ {
		g := fmt.Sprintf("item%d", i)
		if !used[g] {
			return g
		}
	}
}

// Version is the VERSION value ("3.0" when missing).
func (c *Card) Version() string {
	if v := c.Value("VERSION"); v != "" {
		return v
	}
	return "3.0"
}

// UID is the UID value (vCard 4 may write urn:uuid:…; kept as is).
func (c *Card) UID() string { return c.Value("UID") }

// Clone deep-copies the card.
func (c *Card) Clone() *Card {
	out := &Card{Props: make([]*Property, len(c.Props))}
	for i, p := range c.Props {
		q := *p
		q.Params = make([]Param, len(p.Params))
		for j, pr := range p.Params {
			q.Params[j] = Param{Name: pr.Name, Values: append([]string(nil), pr.Values...)}
		}
		out.Props[i] = &q
	}
	return out
}

// ---------- reading ----------

// Parse reads every vCard in data (a .vcf may hold hundreds).
func Parse(data []byte) ([]*Card, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) && !bytes.Contains(bytes.ToUpper(data), []byte("CHARSET=")) {
		// A whole file in a legacy code page: Windows-1250 is what Central
		// European phones and Outlook write; Latin-1 reads anything.
		if dec, err := charmap.Windows1250.NewDecoder().Bytes(data); err == nil {
			data = dec
		}
	}
	lines := unfold(data)
	var cards []*Card
	var cur *Card
	depth := 0
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		p, err := parseLine(ln)
		if err != nil {
			continue // a broken line is skipped, not the whole card
		}
		switch {
		case p.Name == "BEGIN" && strings.EqualFold(p.Value, "VCARD"):
			if cur != nil {
				// A nested card (vCard 2.1 AGENT) — keep it flat inside.
				depth++
				cur.Add(p)
				continue
			}
			cur = &Card{}
		case p.Name == "END" && strings.EqualFold(p.Value, "VCARD"):
			if cur == nil {
				continue
			}
			if depth > 0 {
				depth--
				cur.Add(p)
				continue
			}
			cur.normalize()
			cards = append(cards, cur)
			cur = nil
		default:
			if cur != nil {
				cur.Add(p)
			}
		}
	}
	if len(cards) == 0 {
		return nil, ErrNoCard
	}
	return cards, nil
}

// ParseOne reads a single vCard (a CardDAV resource).
func ParseOne(data []byte) (*Card, error) {
	cards, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if len(cards) != 1 {
		return nil, fmt.Errorf("expected one vCard, found %d", len(cards))
	}
	return cards[0], nil
}

// unfold joins continuation lines (CRLF + space/tab) and vCard 2.1
// quoted-printable soft breaks (a line ending in "=").
func unfold(data []byte) []string {
	raw := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var out []string
	qp := false
	for _, ln := range raw {
		ln = strings.TrimRight(ln, "\r")
		switch {
		case qp && len(out) > 0:
			out[len(out)-1] = strings.TrimSuffix(out[len(out)-1], "=") + strings.TrimLeft(ln, " \t")
		case (strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t")) && len(out) > 0:
			out = append(out[:len(out)-1], out[len(out)-1]+ln[1:])
		default:
			out = append(out, ln)
		}
		last := out[len(out)-1]
		qp = strings.HasSuffix(last, "=") && isQP(last)
	}
	return out
}

func isQP(line string) bool {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return false
	}
	return strings.Contains(strings.ToUpper(line[:i]), "QUOTED-PRINTABLE")
}

func parseLine(ln string) (*Property, error) {
	// name and params end at the first colon outside quotes
	inQ := false
	colon := -1
	for i := 0; i < len(ln); i++ {
		switch ln[i] {
		case '"':
			inQ = !inQ
		case ':':
			if !inQ {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon <= 0 {
		return nil, errors.New("no colon")
	}
	head, value := ln[:colon], ln[colon+1:]
	parts := splitParams(head)
	name := parts[0]
	p := &Property{Value: value}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		p.Group, name = name[:i], name[i+1:]
	}
	p.Name = strings.ToUpper(strings.TrimSpace(name))
	if p.Name == "" {
		return nil, errors.New("no name")
	}
	for _, raw := range parts[1:] {
		if raw == "" {
			continue
		}
		k, v, ok := strings.Cut(raw, "=")
		if !ok {
			// vCard 2.1: TEL;CELL;PREF → TYPE=CELL, TYPE=PREF; QUOTED-PRINTABLE / BASE64 are encodings
			switch u := strings.ToUpper(raw); u {
			case "QUOTED-PRINTABLE", "BASE64", "B", "8BIT", "7BIT":
				k, v = "ENCODING", u
			default:
				k, v = "TYPE", raw
			}
		}
		k = strings.ToUpper(strings.TrimSpace(k))
		var vals []string
		for _, x := range splitParamValues(v) {
			vals = append(vals, strings.Trim(x, `"`))
		}
		merged := false
		for i := range p.Params {
			if p.Params[i].Name == k {
				p.Params[i].Values = append(p.Params[i].Values, vals...)
				merged = true
				break
			}
		}
		if !merged {
			p.Params = append(p.Params, Param{Name: k, Values: vals})
		}
	}
	return p, nil
}

func splitParams(head string) []string {
	var out []string
	inQ := false
	start := 0
	for i := 0; i < len(head); i++ {
		switch head[i] {
		case '"':
			inQ = !inQ
		case ';':
			if !inQ {
				out = append(out, head[start:i])
				start = i + 1
			}
		}
	}
	return append(out, head[start:])
}

func splitParamValues(v string) []string {
	var out []string
	inQ := false
	start := 0
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '"':
			inQ = !inQ
		case ',':
			if !inQ {
				out = append(out, v[start:i])
				start = i + 1
			}
		}
	}
	return append(out, v[start:])
}

// normalize decodes vCard 2.1 quoted-printable and legacy charsets in place
// so that every value is UTF-8 text from here on.
func (c *Card) normalize() {
	for _, p := range c.Props {
		enc := strings.ToUpper(p.Param("ENCODING"))
		if enc == "QUOTED-PRINTABLE" {
			b, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(p.Value)))
			if err == nil {
				p.Value = string(b)
			}
			p.SetParam("ENCODING")
		}
		if cs := p.Param("CHARSET"); cs != "" {
			if dec := decoderFor(cs); dec != nil {
				if b, err := dec.Bytes([]byte(p.Value)); err == nil {
					p.Value = string(b)
				}
			}
			p.SetParam("CHARSET")
		}
		if !utf8.ValidString(p.Value) {
			if b, err := charmap.Windows1250.NewDecoder().Bytes([]byte(p.Value)); err == nil {
				p.Value = string(b)
			}
		}
	}
}

func decoderFor(cs string) *encoding.Decoder {
	switch strings.ToUpper(strings.ReplaceAll(cs, "_", "-")) {
	case "UTF-8", "UTF8", "US-ASCII", "ASCII":
		return nil
	case "ISO-8859-1", "LATIN1":
		return charmap.ISO8859_1.NewDecoder()
	case "ISO-8859-2", "LATIN2":
		return charmap.ISO8859_2.NewDecoder()
	case "ISO-8859-5":
		return charmap.ISO8859_5.NewDecoder()
	case "WINDOWS-1250", "CP1250":
		return charmap.Windows1250.NewDecoder()
	case "WINDOWS-1251", "CP1251":
		return charmap.Windows1251.NewDecoder()
	case "WINDOWS-1252", "CP1252":
		return charmap.Windows1252.NewDecoder()
	}
	return nil
}

// ---------- writing ----------

// String writes the card with CRLF line ends, folded at 75 octets.
func (c *Card) String() string {
	var b strings.Builder
	b.WriteString("BEGIN:VCARD\r\n")
	hasVersion := false
	for _, p := range c.Props {
		if p.Name == "VERSION" {
			hasVersion = true
		}
	}
	if !hasVersion {
		b.WriteString("VERSION:3.0\r\n")
	}
	// VERSION must come right after BEGIN.
	for _, p := range c.Props {
		if p.Name == "VERSION" {
			writeFolded(&b, p.line())
		}
	}
	for _, p := range c.Props {
		if p.Name == "VERSION" {
			continue
		}
		writeFolded(&b, p.line())
	}
	b.WriteString("END:VCARD\r\n")
	return b.String()
}

func (p *Property) line() string {
	var b strings.Builder
	if p.Group != "" {
		b.WriteString(p.Group)
		b.WriteByte('.')
	}
	b.WriteString(p.Name)
	for _, pr := range p.Params {
		b.WriteByte(';')
		b.WriteString(pr.Name)
		b.WriteByte('=')
		for i, v := range pr.Values {
			if i > 0 {
				b.WriteByte(',')
			}
			if strings.ContainsAny(v, ":;,") {
				b.WriteString(`"` + strings.ReplaceAll(v, `"`, "'") + `"`)
			} else {
				b.WriteString(v)
			}
		}
	}
	b.WriteByte(':')
	b.WriteString(strings.NewReplacer("\r\n", `\n`, "\n", `\n`, "\r", "").Replace(p.Value))
	return b.String()
}

func writeFolded(b *strings.Builder, ln string) {
	const max = 75
	first := true
	for len(ln) > 0 {
		limit := max
		if !first {
			limit = max - 1
		}
		if len(ln) <= limit {
			if !first {
				b.WriteByte(' ')
			}
			b.WriteString(ln)
			break
		}
		cut := limit
		for cut > 0 && !utf8.RuneStart(ln[cut]) {
			cut--
		}
		if !first {
			b.WriteByte(' ')
		}
		b.WriteString(ln[:cut])
		b.WriteString("\r\n")
		ln = ln[cut:]
		first = false
	}
	b.WriteString("\r\n")
}

// Join writes several cards as one .vcf file.
func Join(cards []*Card) string {
	var b strings.Builder
	for _, c := range cards {
		b.WriteString(c.String())
	}
	return b.String()
}

// ---------- vCard 2.1 → 3.0 ----------

// Upgrade turns a vCard 2.1 into a 3.0 card (what iOS and Nextcloud speak):
// base64 photos get ENCODING=b, bare type names become TYPE=… (already done
// by the parser), quoted-printable is decoded (done by normalize).
func (c *Card) Upgrade() {
	if c.Version() != "2.1" {
		return
	}
	for _, p := range c.Props {
		switch p.Name {
		case "VERSION":
			p.Value = "3.0"
		}
		if enc := strings.ToUpper(p.Param("ENCODING")); enc == "BASE64" || enc == "B" {
			p.SetParam("ENCODING", "b")
			p.Value = strings.Join(strings.Fields(p.Value), "")
		}
	}
	if c.Get("FN") == nil {
		ct := c.Contact()
		c.Set("FN", ct.DisplayName())
	}
}

// ---------- photo ----------

// Photo returns the embedded picture (data, mime type) or its address.
func (c *Card) Photo() (data []byte, mime, uri string) {
	p := c.Get("PHOTO")
	if p == nil {
		p = c.Get("LOGO")
		if p == nil {
			return nil, "", ""
		}
	}
	v := strings.TrimSpace(p.Value)
	if strings.HasPrefix(v, "data:") {
		head, body, ok := strings.Cut(v[5:], ",")
		if !ok {
			return nil, "", ""
		}
		mime = strings.TrimSuffix(strings.Split(head, ";")[0], ";")
		if strings.Contains(head, ";base64") {
			b, err := decodeB64(body)
			if err != nil {
				return nil, "", ""
			}
			return b, mime, ""
		}
		return []byte(body), mime, ""
	}
	enc := strings.ToUpper(p.Param("ENCODING"))
	if enc == "B" || enc == "BASE64" {
		b, err := decodeB64(v)
		if err != nil {
			return nil, "", ""
		}
		t := strings.ToLower(p.Param("TYPE"))
		if t == "" {
			t = sniffImage(b)
		}
		if !strings.Contains(t, "/") {
			t = "image/" + t
		}
		return b, t, ""
	}
	if strings.HasPrefix(strings.ToLower(v), "http://") || strings.HasPrefix(strings.ToLower(v), "https://") {
		return nil, "", v
	}
	return nil, "", ""
}

func decodeB64(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "")
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
}

func sniffImage(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xff, 0xd8}):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("\x89PNG")):
		return "image/png"
	case bytes.HasPrefix(b, []byte("GIF8")):
		return "image/gif"
	case len(b) > 12 && string(b[8:12]) == "WEBP":
		return "image/webp"
	case len(b) > 12 && string(b[4:12]) == "ftypheic":
		return "image/heic"
	}
	return "image/jpeg"
}

// SetPhoto embeds a picture the way the card's version expects ("" removes it).
func (c *Card) SetPhoto(data []byte, mime string) {
	c.Remove("PHOTO")
	if len(data) == 0 {
		return
	}
	if mime == "" {
		mime = sniffImage(data)
	}
	p := &Property{Name: "PHOTO"}
	if c.Version() == "4.0" {
		p.Value = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	} else {
		p.Params = []Param{{Name: "ENCODING", Values: []string{"b"}}, {Name: "TYPE", Values: []string{strings.ToUpper(strings.TrimPrefix(mime, "image/"))}}}
		p.Value = base64.StdEncoding.EncodeToString(data)
	}
	c.Add(p)
}
