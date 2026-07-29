// Package plan reads the unit backlog: the markdown document `unit.plan` names, split into
// unit blocks by the heading shape `unit.locator` declares.
//
// Both fields have been written by `batten init` since the beginning — derived from the
// backlog's real heading shape — and read back by nobody: unit.locator sat in the
// declared-as-future list as a promise with no consumer (plan §8). This package is the
// consumer. It is deliberately a parser and nothing more: batten does not manage the backlog,
// does not rewrite it, and does not invent structure the document does not have — a document
// with no matching headings yields zero units, and the callers say so out loud rather than
// guessing.
package plan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ArthurZizumbo/batten/internal/spec"
)

// Unit is one work item exactly as the plan document defines it.
type Unit struct {
	ID    string
	Title string // what follows the id on its heading line, stripped of separator punctuation
	Body  string // the block under the heading, up to the next unit or same-level heading
	Line  int    // 1-based line of the heading in the document, for error messages and editors
}

// ErrNoPlan distinguishes "this spec declares no backlog" from "the backlog has no units".
// The two need different advice, so they must not share an error.
var ErrNoPlan = errors.New("batten: unit.plan is not declared in batten.yaml")

// Load resolves unit.plan + unit.locator into the units the backlog actually defines.
func Load(sp *spec.Spec) ([]Unit, error) {
	if sp.Unit.Plan == "" {
		return nil, ErrNoPlan
	}
	path := filepath.Join(sp.Root, filepath.FromSlash(sp.Unit.Plan))
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("batten: unit.plan names %s and it cannot be read: %w", sp.Unit.Plan, err)
	}
	return Parse(string(b), sp.Unit.Locator, sp.Unit.Pattern)
}

// Parse splits a plan document into unit blocks.
//
// locator is the heading template init records — e.g. "### {id}" — where {id} stands for
// unit.pattern. An empty locator degrades to "any markdown heading that begins with a unit
// id", which is the same shape init derives the locator from in the first place.
//
// A block runs from its heading to the next unit heading, or to any heading of the same or
// a shallower level — a "## Fase 2" section break ends the last unit of "## Fase 1".
func Parse(doc, locator, pattern string) ([]Unit, error) {
	if pattern == "" {
		return nil, errors.New("batten: unit.pattern is empty — there is no id shape to look for")
	}

	// Named groups, because unit.pattern is a user regexp and may carry capture groups of
	// its own — positional indexing would silently read the wrong one.
	var src string
	fixedLevel := 0
	if locator == "" {
		src = `^(?P<lvl>#{1,6})\s+(?P<id>` + pattern + `)(?P<rest>.*)$`
	} else {
		pre, post, ok := strings.Cut(locator, "{id}")
		if !ok {
			return nil, fmt.Errorf("batten: unit.locator %q has no {id} placeholder — it cannot locate anything", locator)
		}
		src = `^` + regexp.QuoteMeta(pre) + `(?P<id>` + pattern + `)` + regexp.QuoteMeta(post) + `(?P<rest>.*)$`
		for _, r := range pre {
			if r != '#' {
				break
			}
			fixedLevel++
		}
	}
	headRe, err := regexp.Compile(src)
	if err != nil {
		return nil, fmt.Errorf("batten: unit.locator %q + unit.pattern %q do not compile: %w", locator, pattern, err)
	}
	idIdx, restIdx, lvlIdx := headRe.SubexpIndex("id"), headRe.SubexpIndex("rest"), headRe.SubexpIndex("lvl")
	anyHead := regexp.MustCompile(`^(#{1,6})\s`)

	lines := strings.Split(doc, "\n")
	var units []Unit
	var cur *Unit
	var body []string
	curLevel := 0
	flush := func() {
		if cur != nil {
			cur.Body = strings.TrimRight(strings.Join(body, "\n"), "\n") + "\n"
			units = append(units, *cur)
		}
		cur, body = nil, nil
	}
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if m := headRe.FindStringSubmatch(line); m != nil {
			flush()
			cur = &Unit{ID: m[idIdx], Title: cleanTitle(m[restIdx]), Line: i + 1}
			curLevel = fixedLevel
			if lvlIdx >= 0 {
				curLevel = len(m[lvlIdx])
			}
			continue
		}
		if cur != nil {
			// A same-or-shallower heading is a section break: the unit's block is over even
			// though no new unit has started yet.
			if hm := anyHead.FindStringSubmatch(line); hm != nil && curLevel > 0 && len(hm[1]) <= curLevel {
				flush()
				continue
			}
			body = append(body, line)
		}
	}
	flush()
	return units, nil
}

// Find returns the first unit with the given id.
func Find(units []Unit, id string) (Unit, bool) {
	for _, u := range units {
		if u.ID == id {
			return u, true
		}
	}
	return Unit{}, false
}

// cleanTitle strips the separator punctuation between the id and the human title:
// "— historia 3", ": rate limit", "- fix the gate" all yield the bare title.
func cleanTitle(s string) string {
	return strings.TrimLeft(strings.TrimSpace(s), "—–-:·. \t")
}

// criteriaHead matches the line that introduces the acceptance criteria inside a unit block:
// a heading or a bold line whose text mentions criteria/criterios/acceptance. This is the
// shape real backlogs use (the field-test replica writes `**Criterios de aceptacion**`).
var criteriaHead = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s+|\*\*)?[^*#]*(criteri|acceptance)`)

// listItem matches one bullet, checkbox or numbered list item and captures its text.
var listItem = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+(?:\[[ xX]\]\s+)?(.+?)\s*$`)

// Criteria extracts a unit's acceptance criteria from its block, in document order.
//
// Two shapes count, and nothing else is guessed at:
//  1. list items under a heading or bold line mentioning criteria/acceptance, until the
//     list ends (a heading, a bold line, or prose after the list has started);
//  2. when no such section exists, checkbox items (`- [ ]` / `- [x]`) anywhere in the block —
//     a checkbox is an acceptance criterion by construction, wherever it sits.
//
// A unit with neither yields nil, and the consumers report that as "no criteria declared",
// never as "0 of 0 satisfied" — an empty list is not a passed list.
func (u Unit) Criteria() []string {
	var out []string
	in := false
	for _, raw := range strings.Split(u.Body, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case !in && criteriaHead.MatchString(line):
			in = true
		case in:
			if m := listItem.FindStringSubmatch(line); m != nil {
				out = append(out, m[1])
				continue
			}
			if strings.TrimSpace(line) == "" {
				continue // blank lines inside the list are fine
			}
			if len(out) > 0 {
				return out // prose or a new section after the list: the criteria are over
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	// Fallback: explicit checkboxes anywhere in the block.
	checkbox := regexp.MustCompile(`^\s*[-*+]\s+\[[ xX]\]\s+(.+?)\s*$`)
	for _, raw := range strings.Split(u.Body, "\n") {
		if m := checkbox.FindStringSubmatch(strings.TrimRight(raw, "\r")); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}
