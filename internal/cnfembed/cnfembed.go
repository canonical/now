// Copyright 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package cnfembed is a standalone single-file conformant implementation
// of cnf that translates it to json, preserving order and text style where
// possible. This layout makes it optimal for copying and embedding into
// projects that would like to use cnf for reading configuration files but
// would rather not add an additional external dependency.
//
// References:
// - The format specification: https://github.com/canonical/cnf
// - The source implementation: https://github.com/canonical/go-cnf
package cnfembed

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Options for parsing and rendering. None exist yet,
// here for future compatibility.
type Options struct{}

// ToJSON translates cnf text to json text.
func ToJSON(data []byte, opts *Options) ([]byte, error) {
	root, err := parse(data)
	if err != nil {
		return nil, err
	}
	return []byte(writeNodeString(root)), nil
}

//
// The tree: the whole document in map scopes
//

// A keyNode is one key of one map scope and the value under
// it: a map, a list, or a scalar's pre-encoded json. A bare
// list element carries no key; a map's sub is never nil.
type keyNode struct {
	key      string     // the segment's decoded text; "" for a bare list element
	quote    quoteKind  // the segment's quoting style, to spell it in errors
	pos      pos        // where the segment was written
	sub      []*keyNode // the map or list under this key
	list     bool       // sub holds a list's elements, not a map's pairs
	json     string     // the pre-encoded scalar value; "" = none
	expanded bool       // a map was written here already, once only
	offset   int        // the segment's offset within its scanned path
}

type quoteKind int

const (
	unquoted     quoteKind = iota // bare word
	doubleQuoted                  // "..."
	singleQuoted                  // '...'
)

func (n *keyNode) find(key string) *keyNode {
	for _, e := range n.sub {
		if e.key == key {
			return e
		}
	}
	return nil
}

// walk descends the path's inner segments, creating
// pass-through maps. Only a definition - a scalar or a
// list - blocks a path; a map is traversed, however spelled.
func (scope *keyNode) walk(path []*keyNode) (*keyNode, error) {
	for _, seg := range path[:len(path)-1] {
		node := scope.find(seg.key)
		if node == nil {
			node = &keyNode{key: seg.key, quote: seg.quote, pos: seg.pos, sub: []*keyNode{}}
			scope.sub = append(scope.sub, node)
		} else if node.json != "" || node.list {
			return nil, errAt(seg.pos, "cannot redefine previous key "+quoteKey(node))
		}
		scope = node
	}
	return scope, nil
}

// define places a non-map value: the tip must carry nothing
// yet. A redefinition reports the whole path, anchored at
// its first segment.
func (scope *keyNode) define(path []*keyNode, val *keyNode) error {
	parent, err := scope.walk(path)
	if err != nil {
		return err
	}
	tip := path[len(path)-1]
	if parent.find(tip.key) != nil {
		return redefinePath(tip)
	}
	parent.sub = append(parent.sub, &keyNode{
		key: tip.key, quote: tip.quote, pos: tip.pos,
		json: val.json, list: val.list, sub: val.sub,
	})
	return nil
}

// expand opens the tip's map for its items to be read
// straight into, once only: a map already written there is
// a second expansion.
func (scope *keyNode) expand(path []*keyNode) (*keyNode, error) {
	parent, err := scope.walk(path)
	if err != nil {
		return nil, err
	}
	tip := path[len(path)-1]
	node := parent.find(tip.key)
	if node == nil {
		node = &keyNode{key: tip.key, quote: tip.quote, pos: tip.pos, sub: []*keyNode{}}
		parent.sub = append(parent.sub, node)
	} else if node.json != "" || node.list {
		return nil, redefinePath(tip)
	} else if node.expanded {
		return nil, expandPathErr(tip)
	}
	node.expanded = true
	return node, nil
}

// redefinePath reports a second definition, anchored at the
// redefining segment and naming that segment alone: `b`,
// not the path that reaches it.
func redefinePath(seg *keyNode) error {
	return errAt(seg.pos, "cannot redefine previous key "+quoteKey(seg))
}

// expandPathErr reports a map key's second expansion, anchored
// at the re-expanding segment and naming that segment alone.
func expandPathErr(seg *keyNode) error {
	return errAt(seg.pos, "cannot expand map key "+quoteKey(seg)+" more than once")
}

// quoteKey force-quotes one key segment for an error message.
func quoteKey(seg *keyNode) string {
	return quote(seg.key, seg.quote, true)
}

// wrapPath wraps a keyed element's path around its value:
// one single-pair level per segment, the tip holding it.
func wrapPath(path []*keyNode, val *keyNode) *keyNode {
	n := &keyNode{
		key: path[len(path)-1].key, quote: path[len(path)-1].quote, pos: path[len(path)-1].pos,
		json: val.json, list: val.list, sub: val.sub,
	}
	for i := len(path) - 2; i >= 0; i-- {
		n = &keyNode{
			key: path[i].key, quote: path[i].quote, pos: path[i].pos,
			sub: []*keyNode{n},
		}
	}
	return n
}

// writeNodeString marshals one value node as json text.
func writeNodeString(n *keyNode) string {
	var b strings.Builder
	writeNode(&b, n)
	return b.String()
}

// writeNode writes the value face: a list's elements, a
// map's pairs, or the scalar json. Keys are the parent's
// to write - a map writes them, a list does not.
func writeNode(b *strings.Builder, n *keyNode) {
	switch {
	case n.list:
		b.WriteString("[")
		for i, e := range n.sub {
			if i > 0 {
				b.WriteString(",")
			}
			if e.key != "" {
				// The element's key face: nothing below the list writes
				// keys, so the head's own key wraps its value here.
				b.WriteString("{")
				writeJSONString(b, e.key)
				b.WriteString(":")
				writeNode(b, e)
				b.WriteString("}")
				continue
			}
			writeNode(b, e)
		}
		b.WriteString("]")
	case n.sub != nil:
		b.WriteString("{")
		for i, e := range n.sub {
			if i > 0 {
				b.WriteString(",")
			}
			writeJSONString(b, e.key)
			b.WriteString(":")
			writeNode(b, e)
		}
		b.WriteString("}")
	default:
		b.WriteString(n.json)
	}
}

//
// The source positions
//

// A pos is a source position for errors: 1-based line and column.
type pos struct {
	L int
	C int
}

// String returns the position as "L:C".
func (p pos) String() string {
	return fmt.Sprintf("%d:%d", p.L, p.C)
}

func errAt(p pos, msg string) error {
	return fmt.Errorf("%d:%d: %s", p.L, p.C, msg)
}

// A scanError is an error a byte scanner raises inside the
// text it was scanning: the offset names where the offender
// sits within that text.
type scanError struct {
	offset int
	msg    string
}

func (e scanError) Error() string { return e.msg }

// errAtScan wraps a scan error at the source position where
// the scanned text begins: pos advanced over the scanned text
// the error's offset names.
func errAtScan(p pos, text string, scanErr error) error {
	serr, ok := scanErr.(scanError)
	if !ok {
		return errAt(p, scanErr.Error())
	}
	offset := serr.offset
	if offset > len(text) {
		offset = len(text)
	}
	delta := text[:offset]
	l := p.L + strings.Count(delta, "\n")
	last := strings.LastIndexByte(delta, '\n')
	c := p.C
	if last >= 0 {
		c = utf8.RuneCountInString(delta[last+1:]) + 1
	} else {
		c += utf8.RuneCountInString(delta)
	}
	return errAt(pos{L: l, C: c}, serr.msg)
}

// quoteKeyPath force-quotes a key path for an error message.
func quoteKeyPath(path []*keyNode) string {
	if len(path) == 1 {
		return quoteKey(path[0])
	}
	var b strings.Builder
	b.WriteString("`")
	for i, k := range path {
		if i > 0 {
			b.WriteString(".")
		}
		b.WriteString(quote(k.key, k.quote, false))
	}
	b.WriteString("`")
	return b.String()
}

//
// The document lines
//

// A line is one physical line of the document, split into
// its indentation prefix and its content.
type line struct {
	num    int    // 1-based line number, for errors
	indent string // the literal prefix, spaces or tabs
	text   string // content after the prefix
}

func splitLines(data string) []line {
	if data == "" {
		return nil
	}
	raws := strings.Split(data, "\n")
	if data[len(data)-1] == '\n' {
		raws = raws[:len(raws)-1] // the final newline ends the last line
	}
	lines := make([]line, len(raws))
	for n, raw := range raws {
		if len(raw) > 0 && raw[len(raw)-1] == '\r' {
			raw = raw[:len(raw)-1]
		}
		cut := 0
		for cut < len(raw) && (raw[cut] == ' ' || raw[cut] == '\t') {
			cut++
		}
		lines[n] = line{num: n + 1, indent: raw[:cut], text: raw[cut:]}
	}
	return lines
}

func indentPos(ln line) pos {
	return pos{L: ln.num, C: len(ln.indent) + 1}
}

// quoteShort quotes a short prefix of s for an error message:
// up to five runes as they stand, then "..." when more follow.
func quoteShort(s string) string {
	const cut = 5
	for i := range s {
		if i == cut {
			s = s[:cut] + "..."
			break
		}
	}
	return fmt.Sprintf("`%s`", Escape(s))
}

// Escape escapes s by the double-quote rule, without the quotes.
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteString(escapeCnf(r))
	}
	return b.String()
}

// parse reads a cnf document into its root value node.
func parse(data []byte) (*keyNode, error) {
	if !utf8.Valid(data) {
		return nil, invalidUTF8Err(data)
	}
	p := &parser{lines: splitLines(string(data))}
	root, err := p.parseTop()
	if err != nil {
		return nil, err
	}
	if p.first != nil {
		return nil, p.first
	}
	return root, nil
}

// invalidUTF8Err locates the first invalid byte in data and
// names its line and column, both 1-based, counted in runes
// within the line.
func invalidUTF8Err(data []byte) error {
	line, col, off := 1, 1, 0
	for i := 0; i < len(data); {
		if data[i] == '\n' {
			line, col, off = line+1, 1, i+1
			i++
			continue
		}
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size <= 1 {
			col += utf8.RuneCount(data[off:i])
			return errAt(pos{L: line, C: col}, fmt.Sprintf("cannot decode byte %#x: not UTF-8", data[i]))
		}
		i += size
	}
	// Unreachable: the caller only asks on invalid input.
	return fmt.Errorf("cannot decode document: not UTF-8")
}

//
// The parser: lines read into map scopes
//

type parser struct {
	lines   []line
	pos     int
	pending []line // comment and blank lines, not yet validated
	first   error  // the first key conflict, reported once the document parses whole
}

func (p *parser) saveErr(err error) {
	if p.first == nil {
		p.first = err
	}
}

// peekStruct returns the next structural line, collecting the
// blank and comment lines it skips into the pending run. A
// shallower comment closes the block: returned unconsumed.
func (p *parser) peekStruct(prefix string) (line, bool) {
	for p.pos < len(p.lines) {
		ln := p.lines[p.pos]
		if ln.text == "" {
			p.pending = append(p.pending, ln)
			p.pos++
			continue
		}
		if strings.HasPrefix(ln.text, "#") {
			if len(ln.indent) < len(prefix) {
				return ln, true
			}
			p.pending = append(p.pending, ln)
			p.pos++
			continue
		}
		return ln, true
	}
	return line{}, false
}

// checkPending validates the pending run at the block's exact
// prefix and drops it: json cannot carry comments, so only
// the indent rule stands.
func (p *parser) checkPending(prefix string) error {
	err := cmtIndentOK(p.pending, prefix)
	p.pending = nil
	return err
}

func cmtIndentOK(run []line, parent string) error {
	for _, ln := range run {
		if ln.text != "" && ln.indent != parent {
			return errAt(indentPos(ln), "cannot mix indentation prefixes")
		}
	}
	return nil
}

// cmtNoFencePrefix rejects a pending run before a fence:
// nothing may sit between the pair and the fence that
// opens under it.
func (p *parser) cmtNoFencePrefix() error {
	for _, ln := range p.pending {
		if ln.text != "" {
			return errAt(indentPos(ln), "cannot comment between key and fence")
		}
		return errAt(indentPos(ln), "cannot blank between key and fence")
	}
	return nil
}

// parseTop reads the file's one value into the root node.
func (p *parser) parseTop() (*keyNode, error) {
	first, ok := p.peekStruct("")
	if err := p.checkPending(""); err != nil {
		return nil, err
	}
	if !ok {
		// An empty file is an empty map.
		return &keyNode{sub: []*keyNode{}}, nil
	}
	if looksLikeItem(first.text) {
		// A pair-shaped line opens a map.
		root := &keyNode{sub: []*keyNode{}}
		if err := p.parseMap(root, ""); err != nil {
			return nil, err
		}
		return root, nil
	}
	// Any other value opens on its own line, at column zero.
	if first.indent != "" {
		return nil, errAt(indentPos(first), "cannot indent a top-level value")
	}
	root, err := p.parseBlockValue(first)
	if err != nil {
		return nil, err
	}
	if ln, more := p.peekStruct(""); more {
		// Stray content after the top-level value.
		return nil, errAt(indentPos(ln), "cannot parse content after top-level value: "+quoteShort(ln.text))
	}
	return root, nil
}

// looksLikeItem is a cheap check for a "path: value" line:
// the real scan names the path, not the value, on a bad
// line.
func looksLikeItem(text string) bool {
	i := strings.IndexByte(text, ':')
	if i <= 0 {
		return false
	}
	c := text[0]
	return c == '"' || c == '\'' || c == '.' || isBareRune(rune(c))
}

// isListItem reports whether text opens a block list item.
func isListItem(text string) bool {
	return text == "-" || strings.HasPrefix(text, "- ") || strings.HasPrefix(text, "-\t")
}

// isStringFence reports whether text is a fence delimiter.
func isStringFence(text string) bool {
	return text == `"""` || text == `'''`
}

// parseBlockValue reads the block value opening at first,
// a peeked line.
func (p *parser) parseBlockValue(first line) (*keyNode, error) {
	var v *keyNode
	var err error
	switch {
	case isStringFence(first.text):
		var s string
		s, err = p.parseFence(first, first.text)
		v = &keyNode{json: s}
	case first.text == "---":
		v, err = p.parseListFence(first)
	case isListItem(first.text):
		v, err = p.parseBlockList(first.indent)
	case looksLikeItem(first.text):
		m := &keyNode{sub: []*keyNode{}}
		err = p.parseMap(m, first.indent)
		v = m
	default:
		// An inline value on its only line.
		p.pos++ // consume the value's own line
		v, err = p.parseInline(first, first.text)
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// parseMap reads one block map's pairs at the exact prefix
// into the scope.
func (p *parser) parseMap(scope *keyNode, parentIndent string) error {
	for {
		ln, ok := p.peekStruct(parentIndent)
		if !ok {
			return p.checkPending(parentIndent)
		}
		if ln.indent != parentIndent {
			if len(ln.indent) > len(parentIndent) {
				// The deeper line's own start: the pair that
				// misses the block's prefix is named there.
				return errAt(indentPos(ln), "cannot mix indentation prefixes")
			}
			// A shallower line - structural or comment - closes
			// the block; it stays in the stream for the parent.
			return p.checkPending(parentIndent)
		}
		if err := p.checkPending(parentIndent); err != nil {
			return err
		}
		p.pos++
		if err := p.parseItem(ln, scope); err != nil {
			return err
		}
	}
}

// parseItem reads one "path: value" line into the scope.
// A token at offset i in the line's text sits at column
// len(indent)+1+i.
func (p *parser) parseItem(ln line, scope *keyNode) error {
	base := len(ln.indent) + 1
	path, after, err := scanKeyPath(ln.text)
	if err != nil {
		return errAtScan(pos{L: ln.num, C: base}, ln.text, err)
	}
	if after == "" || after[0] != ':' {
		return errAt(pos{L: ln.num, C: base + len(ln.text) - len(after)}, "cannot find key-value separator `:`")
	}
	for i := range path {
		path[i].pos = pos{L: ln.num, C: base + path[i].offset}
	}
	text := strings.TrimLeft(after[1:], " \t")
	if text != "" {
		rest, err := p.scanPairInto(ln, scope, path, text)
		if err != nil {
			return err
		}
		return trailing(ln, rest)
	}
	next, ok := p.peekStruct(ln.indent)
	if ok && ln.indent != "" && !sameIndentChar(next.indent, ln.indent[0]) {
		return errAt(indentPos(next), "cannot mix indentation prefixes")
	}
	if !ok || len(next.indent) <= len(ln.indent) {
		return errAt(indentPos(ln), "cannot omit the value for "+quoteKeyPath(path))
	}
	switch {
	case isListItem(next.text):
		// A dashed block: a value defined under the path
		// like any other.
	case isStringFence(next.text) || next.text == "---":
		// Nothing may sit between the pair and its fence.
		if err := p.cmtNoFencePrefix(); err != nil {
			return err
		}
	default:
		// A block map: the path expands once, and its items
		// read straight into the expansion. A conflict is kept,
		// not raised: the items still parse, into a scope the
		// error path drops.
		tip, err := scope.expand(path)
		if err != nil {
			p.saveErr(err)
			tip = &keyNode{}
		}
		return p.parseMap(tip, next.indent)
	}
	val, err := p.parseBlockValue(next)
	if err != nil {
		return err
	}
	if err := scope.define(path, val); err != nil {
		p.saveErr(err)
	}
	return nil
}

// scanPairInto reads a pair's inline value: an inline map
// expands the path, anything else is defined under it.
func (p *parser) scanPairInto(ln line, scope *keyNode, path []*keyNode, text string) (string, error) {
	if text != "" && text[0] == '{' {
		tip, err := scope.expand(path)
		if err != nil {
			p.saveErr(err)
			tip = &keyNode{}
		}
		pt := pos{L: ln.num, C: len(ln.text) - len(text) + len(ln.indent) + 1}
		return p.scanInlineMapInto(ln, text, pt, tip)
	}
	val, rest, err := p.scanValueAt(ln, text)
	if err != nil {
		return "", err
	}
	if err := scope.define(path, val); err != nil {
		p.saveErr(err)
	}
	return rest, nil
}

// parseBlockList reads one block list's "- item" lines at
// the exact prefix, keyed elements' paths wrapped around
// their values.
func (p *parser) parseBlockList(prefix string) (*keyNode, error) {
	var elems []*keyNode
	for {
		ln, ok := p.peekStruct(prefix)
		if !ok || ln.indent != prefix || !isListItem(ln.text) {
			if err := p.checkPending(prefix); err != nil {
				return nil, err
			}
			return &keyNode{list: true, sub: elems}, nil
		}
		if err := p.checkPending(prefix); err != nil {
			return nil, err
		}
		p.pos++
		text := strings.TrimLeft(ln.text[1:], " \t")
		if text == "" {
			// A dash with nothing after it.
			return nil, errAt(indentPos(ln), "cannot omit the value")
		}
		var path []*keyNode
		// The inlinepair form: a path and a colon before the
		// value; without them the whole text is the value.
		if k, rest, kerr := scanKeyPath(text); kerr == nil && rest != "" && rest[0] == ':' {
			off := len(ln.text) - len(text)
			for i := range k {
				k[i].pos = pos{L: ln.num, C: len(ln.indent) + 1 + off + k[i].offset}
			}
			path = k
			text = strings.TrimLeft(rest[1:], " \t")
			if text == "" {
				// A path ending on its colon: the same omission
				// parseItem reports.
				return nil, errAt(indentPos(ln), "cannot omit the value for "+quoteKeyPath(path))
			}
		}
		val, err := p.parseInline(ln, text)
		if err != nil {
			return nil, err
		}
		if path != nil {
			val = wrapPath(path, val)
		}
		elems = append(elems, val)
	}
}

// parseFence reads one fence block from its opening line,
// already peeked as next, into its json string. In the
// escaped family a trailing backslash continues the line.
func (p *parser) parseFence(next line, delim string) (string, error) {
	prefix := next.indent
	p.pos++ // consume the opening delimiter
	escaped := delim == `"""`
	var b strings.Builder
	b.WriteByte('"')
	indentChar := byte(' ')
	if len(prefix) > 0 {
		indentChar = prefix[0]
	}
	continued := false
	for p.pos < len(p.lines) {
		ln := p.lines[p.pos]
		p.pos++
		// The close wins even over a continuation: the
		// escape ate the newline, the value ends without one.
		if ln.indent == prefix && ln.text == delim {
			b.WriteByte('"')
			return b.String(), nil
		}
		if !continued && !sameIndentChar(ln.indent, indentChar) {
			return "", errAt(indentPos(ln), "cannot mix indentation prefixes in fence")
		}
		if len(ln.indent) < len(prefix) {
			if ln.text != "" {
				// Only the exact prefix closes the fence.
				return "", errAt(indentPos(ln), "cannot close fence at shallower prefix")
			}
			// A blank line with a valid but shallower prefix:
			// the line contributes the newline only.
			b.WriteString(`\n`)
			continue
		}
		if ln.text == "" {
			// A blank line at or deeper than the fence prefix:
			// its whitespace is content, with its terminator.
			writeEscapedJSON(&b, ln.indent[len(prefix):])
			b.WriteString(`\n`)
			continue
		}
		if !strings.HasPrefix(ln.indent, prefix) {
			return "", errAt(indentPos(ln), "cannot mix indentation prefixes in fence")
		}
		// The content following the valid prefix is the string,
		// including any excess indentation on deeper lines.
		content := ln.indent[len(prefix):] + ln.text
		contentPos := pos{L: ln.num, C: len(prefix) + 1}
		continued = false
		if escaped && strings.IndexByte(content, '\\') >= 0 {
			// Only a backslash can unescape or continue a line;
			// plain content flows to the reescaper as it stands.
			if oddTrailingBackslashes(content) {
				content = content[:len(content)-1]
				continued = true
			}
			if err := writeReescapedJSON(&b, content); err != nil {
				return "", errAtScan(contentPos, content, err)
			}
			if !continued {
				b.WriteString(`\n`)
			}
			continue
		}
		writeEscapedJSON(&b, content)
		if !continued {
			b.WriteString(`\n`)
		}
	}
	return "", errAt(indentPos(next), "cannot find closing fence")
}

// sameIndentChar reports whether the prefix consists solely
// of the given indent character.
func sameIndentChar(prefix string, c byte) bool {
	for i := 0; i < len(prefix); i++ {
		if prefix[i] != c {
			return false
		}
	}
	return true
}

// oddTrailingBackslashes reports whether the text ends on an
// odd run of backslashes - an escape that ate the newline.
func oddTrailingBackslashes(s string) bool {
	n := 0
	for n < len(s) && s[len(s)-1-n] == '\\' {
		n++
	}
	return n%2 == 1
}

// parseListFence reads one --- delimited list from its
// opening line, each element the lines between two ---
// at the fence's exact prefix, parsed as a block value.
func (p *parser) parseListFence(next line) (*keyNode, error) {
	prefix := next.indent
	p.pos++ // consume the opening ---
	var elems []*keyNode
	nested := "" // the indent of the fence's nested elements, once set
	for {
		start := p.pos
		closed := false
		for p.pos < len(p.lines) {
			cur := p.lines[p.pos]
			if cur.indent == prefix && cur.text == "---" {
				closed = true
				break
			}
			p.pos++
		}
		if !closed {
			// The fence that never closes is named where it
			// opened: its own position.
			return nil, errAt(indentPos(next), "cannot find closing list fence")
		}
		elem := p.lines[start:p.pos]
		if len(elem) == 0 {
			// The error names the delimiter that opened the
			// element: the previous element's close, or the
			// fence's own opening line.
			open := next
			if start > 0 {
				open = p.lines[start-1]
			}
			return nil, errAt(indentPos(open), "cannot parse empty list fence element")
		}
		// Nested fences must share one indent: the first
		// sets it, a mismatched one mixes indents inside
		// the fence.
		if elem[0].text == "---" && len(elem[0].indent) > len(prefix) {
			if nested == "" {
				nested = elem[0].indent
			} else if elem[0].indent != nested {
				return nil, errAt(indentPos(elem[0]), "cannot mix indents inside list fence")
			}
		}
		p.pos++
		sub := &parser{lines: elem}
		first, ok := sub.peekStruct(prefix)
		if !ok {
			open := next
			if start > 0 {
				open = p.lines[start-1]
			}
			return nil, errAt(indentPos(open), "cannot parse empty list fence element")
		}
		// The fence's prefix delimits the element: a deeper
		// line opens a nested block only as a list fence.
		if first.indent != prefix && !(first.text == "---" && len(first.indent) > len(prefix)) {
			return nil, errAt(indentPos(first), "list element indent must align with fence")
		}
		val, err := sub.parseBlockValue(first)
		if err != nil {
			return nil, err
		}
		p.saveErr(sub.first) // the element's own conflicts count too
		if sub.pos != len(sub.lines) {
			// Content after the element's value.
			return nil, errAt(indentPos(sub.lines[sub.pos]), "cannot parse content in list fence element")
		}
		elems = append(elems, val)
		// The fence continues while the next line sits at its
		// prefix or deeper; a shallower line closes it and
		// stays in the stream for the parent.
		if ln, ok := p.peekStruct(prefix); !ok || len(ln.indent) < len(prefix) {
			return &keyNode{list: true, sub: elems}, nil
		}
	}
}

// parseInline reads one inline value on the item's line into
// its node.
func (p *parser) parseInline(ln line, text string) (*keyNode, error) {
	val, rest, err := p.scanValueAt(ln, text)
	if err != nil {
		return nil, err
	}
	return val, trailing(ln, rest)
}

// trailing accepts what follows a value on its line: nothing,
// or a comment.
func trailing(ln line, rest string) error {
	left, _, ok := stripTrail(rest)
	if ok || left == "" {
		return nil
	}
	return errAt(pos{L: ln.num, C: len(ln.indent) + 1 + len(ln.text) - len(left)}, fmt.Sprintf("cannot parse trailing `%s`", Escape(left)))
}

// stripTrail splits a trailing comment - at least one blank,
// then # - off the rest of a value. A # glued to the value
// is content, not a comment.
func stripTrail(rest string) (left, cmt string, ok bool) {
	i := 0
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
		i++
	}
	if i == 0 || i >= len(rest) || rest[i] != '#' {
		return rest, "", false
	}
	return "", rest, true
}

// scanValueAt reads one inline value from the front of text
// into its node, returning the unread remainder.
func (p *parser) scanValueAt(ln line, text string) (*keyNode, string, error) {
	pt := pos{L: ln.num, C: len(ln.text) - len(text) + len(ln.indent) + 1}
	if text == "" {
		// An empty scan: the usual source is an unclosed
		// list's spent tail.
		return nil, "", errAt(pt, "cannot parse value at \"\"")
	}
	switch text[0] {
	case '{':
		// A fresh scope: nothing later reaches into a
		// value-position map.
		m := &keyNode{sub: []*keyNode{}}
		rest, err := p.scanInlineMapInto(ln, text, pt, m)
		if err != nil {
			return nil, "", err
		}
		return m, rest, nil
	case '[':
		return p.scanInlineList(ln, text, pt)
	case '"':
		if strings.HasPrefix(text, `"""`) {
			return p.scanFencedLine(ln, text, `"`, pt)
		}
		s, rest, err := scanDoubleQuoted(text)
		if err != nil {
			return nil, "", errAtScan(pt, text, err)
		}
		return &keyNode{json: jsonString(s)}, rest, nil
	case '\'':
		if strings.HasPrefix(text, `'''`) {
			return p.scanFencedLine(ln, text, `'`, pt)
		}
		s, rest, err := scanSingleQuoted(text)
		if err != nil {
			return nil, "", errAt(pt, err.Error())
		}
		return &keyNode{json: jsonString(s)}, rest, nil
	}
	tok, rest := scanScalarToken(text)
	switch tok {
	case "null", "true", "false":
		return &keyNode{json: tok}, rest, nil
	case "":
		// The text starts outside every scanned class.
		return nil, "", errAt(pt, fmt.Sprintf("cannot parse value at `%s`", Escape(text)))
	}
	val, err := scanNumber(tok, pt)
	if err != nil {
		return nil, "", err
	}
	return val, rest, nil
}

// scanNumber validates a number-shaped token and encodes
// its node. A bare word is never a value; based forms
// convert to decimal on the spot.
func scanNumber(tok string, pt pos) (*keyNode, error) {
	body := strings.TrimPrefix(tok, "-")
	if body == "" || body[0] < '0' || body[0] > '9' {
		return nil, errAt(pt, fmt.Sprintf("cannot parse unquoted value `%s`", Escape(tok)))
	}
	if len(body) > 2 && body[0] == '0' {
		bases := []byte{'x', 16, 'o', 8, 'b', 2}
		for i := 0; i < len(bases); i += 2 {
			if bases[i] != body[1] {
				continue
			}
			base := int(bases[i+1])
			digits := body[2:]
			for _, c := range digits {
				if v := digitVal(c); v < 0 || v >= base {
					return nil, errAt(pt, fmt.Sprintf("cannot parse number `%s`", Escape(tok)))
				}
			}
			json := baseToDecimal(digits, base)
			if len(body) != len(tok) {
				json = "-" + json // the sign the scanner cut off
			}
			return &keyNode{json: json}, nil
		}
	}
	if strings.ContainsRune(body, 'E') {
		return nil, errAt(pt, fmt.Sprintf("cannot parse number `%s`: uppercase exponent", Escape(tok)))
	}
	if len(body) > 1 && body[0] == '0' && body[1] >= '0' && body[1] <= '9' {
		return nil, errAt(pt, fmt.Sprintf("cannot parse number `%s`: redundant leading zero", Escape(tok)))
	}
	if !isDecimalShape(body) {
		return nil, errAt(pt, fmt.Sprintf("cannot parse number `%s`", Escape(tok)))
	}
	return &keyNode{json: tok}, nil
}

// scanInlineList reads "[1, 2]" in the one-line form into a list node.
func (p *parser) scanInlineList(ln line, text string, at pos) (*keyNode, string, error) {
	var elems []*keyNode
	text = strings.TrimLeft(text[1:], " \t")
	if strings.HasPrefix(text, "]") {
		return &keyNode{list: true}, text[1:], nil
	}
	for {
		val, rest, err := p.scanValueAt(ln, text)
		if err != nil {
			return nil, "", err
		}
		elems = append(elems, val)
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			// Named where it opened, so nested lists tell apart.
			return nil, "", errAt(at, "cannot find closing `]`")
		}
		if rest[0] == ']' {
			return &keyNode{list: true, sub: elems}, rest[1:], nil
		}
		if rest[0] != ',' {
			return nil, "", errAt(pos{L: ln.num, C: len(ln.indent) + 1 + len(ln.text) - len(rest)}, fmt.Sprintf("cannot parse `%s` inside list", Escape(rest)))
		}
		text = strings.TrimLeft(rest[1:], " \t")
	}
}

// scanInlineMapInto reads "{ a: 1, b: 2 }" in the native item
// grammar into the scope, returning the unread remainder.
func (p *parser) scanInlineMapInto(ln line, text string, at pos, scope *keyNode) (string, error) {
	text = strings.TrimLeft(text[1:], " \t")
	if strings.HasPrefix(text, "}") {
		return text[1:], nil
	}
	for {
		off := len(ln.text) - len(text)
		path, after, err := scanKeyPath(text)
		if err != nil {
			return "", errAtScan(pos{L: ln.num, C: len(ln.indent) + 1 + off}, text, err)
		}
		if after == "" || after[0] != ':' {
			return "", errAt(pos{L: ln.num, C: len(ln.indent) + 1 + off + len(text) - len(after)}, "cannot find key-value separator `:`")
		}
		for i := range path {
			path[i].pos = pos{L: ln.num, C: len(ln.indent) + 1 + off + path[i].offset}
		}
		rest, err := p.scanPairInto(ln, scope, path, strings.TrimLeft(after[1:], " \t"))
		if err != nil {
			return "", err
		}
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			// Named where it opened, so nested maps tell apart.
			return "", errAt(at, "cannot find closing `}`")
		}
		if rest[0] == '}' {
			return rest[1:], nil
		}
		if rest[0] != ',' {
			return "", errAt(pos{L: ln.num, C: len(ln.indent) + 1 + len(ln.text) - len(rest)}, fmt.Sprintf("cannot parse `%s` inside map", Escape(rest)))
		}
		text = strings.TrimLeft(rest[1:], " \t")
	}
}

// scanFencedLine reads a one-line fence. The first delimiter
// closes; in the escaped family the search skips escapes.
func (p *parser) scanFencedLine(ln line, text, quote string, at pos) (*keyNode, string, error) {
	delim := strings.Repeat(quote, 3)
	end := -1
	if quote == `"` {
		i := 3
		for i <= len(text)-3 {
			if text[i] == '\\' {
				i += 2 // skip the escape; its quote is content
				continue
			}
			if text[i:i+3] == delim {
				end = i - 3
				break
			}
			i++
		}
	} else {
		end = strings.Index(text[3:], delim)
	}
	if end < 0 {
		return nil, "", errAt(at, "cannot find closing fence")
	}
	inner := text[3 : 3+end]
	innerPos := pos{L: at.L, C: at.C + 3}
	rest := text[3+end+3:]
	if !needsEscape(inner) {
		return &keyNode{json: `"` + inner + `"`}, rest, nil
	}
	if quote == `"` {
		var b strings.Builder
		b.Grow(len(inner) + 2)
		b.WriteByte('"')
		if err := writeReescapedJSON(&b, inner); err != nil {
			return nil, "", errAtScan(innerPos, inner, err)
		}
		b.WriteByte('"')
		return &keyNode{json: b.String()}, rest, nil
	}
	// The raw family holds the interior as it stands.
	return &keyNode{json: jsonString(text[3 : 3+end])}, rest, nil
}

//
// The byte scanners: the grammar's tokens, errors carrying the offender's offset.
//

// scanKeyPath reads a dotted path, recording each segment's
// offset within the original text for position tracking.
// Spaces never separate path parts.
func scanKeyPath(text string) ([]*keyNode, string, error) {
	if text == "" || text[0] == '.' {
		return nil, "", scanError{0, "cannot parse empty key segment"}
	}
	var segs []*keyNode
	consumed := 0
	for {
		seg, after, err := scanKey(text)
		if err != nil {
			// The segment's offset must move into the whole
			// path's frame.
			if serr, ok := err.(scanError); ok {
				serr.offset += consumed
				return nil, "", serr
			}
			return nil, "", err
		}
		seg.offset = consumed
		segs = append(segs, seg)
		consumed += len(text) - len(after)
		if after != "" && after[0] == '.' {
			if len(after) == 1 {
				return nil, "", scanError{consumed + 1, "cannot parse empty key segment"}
			}
			if after[1] == ' ' || after[1] == '\t' {
				return nil, "", scanError{consumed + 1, "cannot parse space in path"}
			}
			text = after[1:]
			consumed++
			continue
		}
		if after != "" && (after[0] == ' ' || after[0] == '\t') {
			return nil, "", scanError{consumed, "cannot parse space in path"}
		}
		return segs, after, nil
	}
}

// scanKey reads one key segment: a bare word or a quoted string.
func scanKey(text string) (*keyNode, string, error) {
	if text == "" {
		return nil, "", scanError{0, "cannot parse empty key segment"}
	}
	switch text[0] {
	case '"':
		s, rest, err := scanDoubleQuoted(text)
		if err != nil {
			return nil, "", err
		}
		return &keyNode{key: s, quote: doubleQuoted}, rest, nil
	case '\'':
		s, rest, err := scanSingleQuoted(text)
		if err != nil {
			return nil, "", err
		}
		return &keyNode{key: s, quote: singleQuoted}, rest, nil
	}
	seg, rest := scanWord(text)
	if seg == "" {
		return nil, "", scanError{0, "cannot parse empty key segment"}
	}
	for _, r := range seg {
		if !isBareRune(r) {
			// Named at the segment start, so the path's
			// consumed count locates it.
			return nil, "", scanError{0, fmt.Sprintf("cannot parse key `%s`", Escape(seg))}
		}
	}
	return &keyNode{key: seg, quote: unquoted}, rest, nil
}

// scanDoubleQuoted reads a double-quoted string, processing escapes.
// The common escape-free string is returned as a slice of the input.
func scanDoubleQuoted(text string) (string, string, error) {
	if i := strings.IndexAny(text[1:], `"\\`); i >= 0 && text[1+i] != '\\' {
		return text[1 : 1+i], text[2+i:], nil
	}
	var b strings.Builder
	i := 1
	for i < len(text) {
		c := text[i]
		switch {
		case c == '"':
			return b.String(), text[i+1:], nil
		case c == '\\':
			if i+1 >= len(text) {
				return "", "", scanError{i, "cannot end string on an escape"}
			}
			r, n, err := scanEscape(text[i:])
			if err != nil {
				return "", "", scanError{i, err.Error()}
			}
			b.WriteRune(r)
			i += n
		default:
			b.WriteByte(c)
			i++
		}
	}
	return "", "", fmt.Errorf("cannot find closing quote")
}

// scanSingleQuoted reads a single-quoted string: raw, no escapes.
func scanSingleQuoted(text string) (string, string, error) {
	end := strings.IndexByte(text[1:], '\'')
	if end < 0 {
		return "", "", fmt.Errorf("cannot find closing quote")
	}
	return text[1 : 1+end], text[2+end:], nil
}

// scanEscape reads one escaped character.
func scanEscape(text string) (rune, int, error) {
	if len(text) < 2 || text[0] != '\\' {
		return 0, 0, fmt.Errorf("cannot parse escape")
	}
	c := text[1]
	switch c {
	case '"', '\\':
		return rune(c), 2, nil
	case 'n':
		return '\n', 2, nil
	case 'r':
		return '\r', 2, nil
	case 't':
		return '\t', 2, nil
	case 'b':
		return '\b', 2, nil
	case 'f':
		return '\f', 2, nil
	case 'u':
		return scanUnicode(text)
	case 'x':
		// Two hex digits must follow: text[2:4] slices them,
		// so four bytes must be present.
		if len(text) >= 4 {
			if v, err := parseHex(text[2:4]); err == nil {
				return rune(v), 4, nil
			}
		}
		return 0, 0, fmt.Errorf("cannot parse \\x escape")
	}
	return 0, 0, fmt.Errorf("cannot parse escape \\%c", c)
}

// scanUnicode reads one \u escape, both \u0000 and \u{...}.
func scanUnicode(text string) (r rune, n int, err error) {
	if len(text) >= 5 && text[2] == '{' {
		// \u{0}
		i := strings.IndexByte(text, '}')
		if i > 3 && i < 10 {
			n = i + 1
			text = text[3:i]
		}
	} else if len(text) >= 6 {
		// \u0000
		n = 6
		text = text[2:6]
	}
	if n > 0 {
		v, err := parseHex(text)
		if err == nil && isUnicodeEscape(v) {
			return rune(v), n, nil
		}
	}
	return 0, 0, fmt.Errorf("cannot parse \\u escape")
}

func isUnicodeEscape(v int64) bool {
	return v <= 0x10FFFF && (v < 0xD800 || v > 0xDFFF)
}

func parseHex(s string) (int64, error) {
	var v int64
	for i := 0; i < len(s); i++ {
		var d int64
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			d = int64(c - '0')
		case c >= 'a' && c <= 'f':
			d = int64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int64(c-'A') + 10
		default:
			return 0, fmt.Errorf("bad hex")
		}
		v = v*16 + d
	}
	return v, nil
}

func scanWord(text string) (word, rest string) {
	cut := 0
	for cut < len(text) && !strings.ContainsRune(" \t,]}.:", rune(text[cut])) {
		cut++
	}
	return text[:cut], text[cut:]
}

func isBareRune(r rune) bool {
	return r == '-' || r == '_' ||
		('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')
}

func digitVal(c rune) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// scanScalarToken reads the maximal token that could spell
// a scalar; the caller tells number, keyword, or bare word.
func scanScalarToken(text string) (tok, rest string) {
	cut := 0
	if cut < len(text) && text[cut] == '-' {
		cut++
	}
	for cut < len(text) {
		c := text[cut]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '.':
			cut++
		case (c == '+' || c == '-') && cut > 0 && (text[cut-1] == 'e' || text[cut-1] == 'E'):
			cut++
		default:
			return text[:cut], text[cut:]
		}
	}
	return text, ""
}

// isDecimalShape matches the grammar's decimal spellings:
// numint [ "." 1*DIGIT ] [ "e" ["+" / "-"] 1*DIGIT ]. The
// redundant leading zero gets its own message.
func isDecimalShape(body string) bool {
	mantissa := body
	if i := strings.IndexByte(body, 'e'); i >= 0 {
		mantissa = body[:i]
		exp := body[i+1:]
		if exp == "" {
			return false
		}
		if exp[0] == '+' || exp[0] == '-' {
			exp = exp[1:]
		}
		if !allDigits(exp) {
			return false
		}
	}
	intPart := mantissa
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		intPart = mantissa[:i]
		if !allDigits(mantissa[i+1:]) {
			return false
		}
	}
	return intPart != "" && allDigits(intPart)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

//
// The json encoding of the leaf values
//

func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	writeEscapedJSON(b, s)
	b.WriteByte('"')
}

func writeEscapedJSON(b *strings.Builder, s string) {
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		if start < i {
			b.WriteString(s[start:i])
		}
		b.WriteString(jsonEscapes[c])
		start = i + 1
	}
	b.WriteString(s[start:])
}

// writeReescapedJSON unescapes s and reescapes as json in one pass.
func writeReescapedJSON(b *strings.Builder, s string) error {
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			start := i
			for i < len(s) && s[i] != '\\' {
				i++
			}
			writeEscapedJSON(b, s[start:i])
			continue
		}
		if i+1 >= len(s) {
			return scanError{i, "cannot end line on an escape"}
		}
		r, n, err := scanEscape(s[i:])
		if err != nil {
			return scanError{i, err.Error()}
		}
		if r < utf8.RuneSelf {
			if esc := jsonEscapes[r]; esc != "" {
				b.WriteString(esc)
			} else {
				b.WriteByte(byte(r))
			}
		} else {
			b.WriteRune(r)
		}
		i += n
	}
	return nil
}

var jsonEscapes = [256]string{
	'"':  `\"`,
	'\\': `\\`,
	'\n': `\n`,
	'\r': `\r`,
	'\t': `\t`,
	'\b': `\b`,
	'\f': `\f`,
	0x00: `\u0000`, 0x01: `\u0001`, 0x02: `\u0002`, 0x03: `\u0003`,
	0x04: `\u0004`, 0x05: `\u0005`, 0x06: `\u0006`, 0x07: `\u0007`,
	0x0b: `\u000b`,
	0x0e: `\u000e`, 0x0f: `\u000f`,
	0x10: `\u0010`, 0x11: `\u0011`, 0x12: `\u0012`, 0x13: `\u0013`,
	0x14: `\u0014`, 0x15: `\u0015`, 0x16: `\u0016`, 0x17: `\u0017`,
	0x18: `\u0018`, 0x19: `\u0019`, 0x1a: `\u001a`, 0x1b: `\u001b`,
	0x1c: `\u001c`, 0x1d: `\u001d`, 0x1e: `\u001e`, 0x1f: `\u001f`,
}

func jsonString(s string) string {
	if !needsEscape(s) {
		return `"` + s + `"`
	}
	var b strings.Builder
	b.Grow(len(s) + 2)
	writeJSONString(&b, s)
	return b.String()
}

func needsEscape(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == '"' || s[i] == '\\' {
			return true
		}
	}
	return false
}

// baseToDecimal converts a digit string - already validated
// by the scanner - to decimal by repeated division by 10:
// one decimal digit per pass, least significant first.
func baseToDecimal(digits string, base int) string {
	var out []byte
	for digits != "" {
		carry := 0
		var quot []byte
		for _, c := range digits {
			cur := carry*base + digitVal(c)
			q := cur / 10
			if q < 10 {
				quot = append(quot, '0'+byte(q))
			} else {
				quot = append(quot, 'a'+byte(q-10))
			}
			carry = cur % 10
		}
		// Strip the quotient's leading zeros; an empty
		// quotient ends the loop.
		i := 0
		for i < len(quot) && quot[i] == '0' {
			i++
		}
		digits = string(quot[i:])
		out = append(out, '0'+byte(carry))
	}
	// The digits came out least significant first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

//
// The cnf quote spelling, for the error messages that name a key path.
//

// quote returns s in the q family or an alternate if that's not possible.
// When force is set, unquoted strings are always promoted to quoted.
func quote(s string, q quoteKind, force bool) string {
	switch q {
	case unquoted:
		if !force && isBareWord(s) {
			return s
		}
		q = doubleQuoted
		if strings.ContainsRune(s, '"') || strings.ContainsRune(s, '\\') {
			// The single family spells both raw, the cleaner
			// spelling - when it holds the rest of the content.
			q = singleQuoted
			if strings.ContainsRune(s, '\'') || strings.ContainsRune(s, '\n') {
				q = doubleQuoted
			}
		}
	case singleQuoted:
		if strings.ContainsRune(s, '\'') || strings.ContainsRune(s, '\n') {
			q = doubleQuoted
		}
	}
	if q == doubleQuoted {
		var b strings.Builder
		b.WriteRune('"')
		for _, r := range s {
			b.WriteString(escapeCnf(r))
		}
		b.WriteRune('"')
		return b.String()
	}
	if q == singleQuoted {
		return "'" + s + "'"
	}
	return s
}

// isBareWord reports whether s can spell as one: non-empty,
// every rune in the bare set.
func isBareWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isBareRune(r) {
			return false
		}
	}
	return true
}

// escapeCnf spells one rune inside a double-quoted cnf
// string: the closed escape set, \xNN canonical for the C0
// controls.
func escapeCnf(r rune) string {
	switch {
	case r == '"':
		return `\"`
	case r == '\\':
		return `\\`
	case r == '\n':
		return `\n`
	case r == '\r':
		return `\r`
	case r == '\t':
		return `\t`
	case r == '\b':
		return `\b`
	case r == '\f':
		return `\f`
	case r < 0x20:
		return fmt.Sprintf(`\x%02x`, r)
	case r == 0x2028 || r == 0x2029:
		return fmt.Sprintf(`\u%04x`, r)
	}
	return string(r)
}
