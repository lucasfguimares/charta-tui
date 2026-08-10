package sqleditor

import "sync"

// Document owns text/version and the most recent immutable analysis. Layout,
// cursor and viewport live in the TUI and are therefore unaffected by parsing.
type Document struct {
	mu       sync.RWMutex
	text     string
	version  uint64
	analysis Analysis
}

func NewDocument(text string) *Document { return &Document{text: text, version: 1} }
func (d *Document) Text() string        { d.mu.RLock(); defer d.mu.RUnlock(); return d.text }
func (d *Document) Version() uint64     { d.mu.RLock(); defer d.mu.RUnlock(); return d.version }
func (d *Document) SetText(text string) uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if text == d.text {
		return d.version
	}
	d.text = text
	d.version++
	return d.version
}
func (d *Document) Apply(analysis Analysis) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if analysis.Version != d.version {
		return false
	}
	d.analysis = analysis
	return true
}
func (d *Document) Analysis() Analysis { d.mu.RLock(); defer d.mu.RUnlock(); return d.analysis }
