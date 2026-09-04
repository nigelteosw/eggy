package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/nigelteosw/eggy/plugins/filelock"
	"gopkg.in/yaml.v3"
)

// This is the other half of retiredConfigFields. That list removes settings a
// build no longer reads, so an upgrade does not turn into a hand-edit on a
// mounted volume; this one adds settings a build has started reading, for
// exactly the same reason. Without it a new section exists only in
// config.example.yaml, which is not the file the owner is running.
//
// The invariant is that a backfill never changes what the config means. Each
// entry writes the defaults that were already in force the moment the section
// was absent, so the file stops being silent about a setting and starts
// describing one that was already true. That is what makes it safe to do
// automatically, and it is what backfillPreservesMeaning tests.
//
// A section belongs here when it is new and its default is worth the owner
// seeing. One whose default is "off and costs nothing" does not: writing an
// empty google: block into every config would be noise, and the absence
// already says what it means.

// defaultedSection is one section this build reads that an older config may
// have no key for.
type defaultedSection struct {
	// key is the top-level name. Sections rather than individual fields is
	// deliberate: filling in every optional field would rewrite the owner's
	// file into a wall of defaults and bury the handful of settings they
	// actually chose.
	key string
	// comment introduces the section in the file, the way config.example.yaml
	// does. An owner who finds a new block in their config should not have to
	// go looking for what it is.
	comment string
	// value is what to write, already carrying the defaults that applied
	// while the section was missing.
	value func() any
}

var defaultedSections = []defaultedSection{
	{
		key: "tracing",
		comment: `The turn trace: every model call with the prompt that produced it,
every tool call with its arguments and output, kept so you can see what Eggy
actually did rather than only what it replied. Read it in the web panel under
Traces.
On unless you turn it off. Full prompt bodies are the largest thing Eggy
writes, so keep_turns and retention are what make that affordable -- and a
prompt carries USER.md, MEMORY.md and your recent conversation, so it should
not sit on disk forever either.`,
		value: func() any {
			// Built from applyDefaults rather than restating the numbers, so
			// the defaults live in exactly one place and a section written
			// into an owner's file can never disagree with the one the
			// loader would have applied to its absence.
			var cfg Config
			_ = cfg.applyDefaults()
			enabled := true
			cfg.Tracing.Enabled = &enabled
			return cfg.Tracing
		},
	},
}

// backfillDefaultedSections adds any section this build reads that path has no
// key for, leaving every existing key, value, and comment exactly as the owner
// wrote it. A config that already names them all is not rewritten at all.
func backfillDefaultedSections(path string) error {
	return filelock.With(path, func() error {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("open config: %w", err)
		}
		var document yaml.Node
		if err := yaml.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("decode config: %w", err)
		}
		if len(document.Content) == 0 {
			return nil
		}
		root := document.Content[0]
		if root.Kind != yaml.MappingNode {
			return nil
		}
		added := make([]string, 0, len(defaultedSections))
		for _, section := range defaultedSections {
			// An owner who wrote the section themselves owns it, whatever
			// they put in it -- including an empty block, which is a
			// deliberate "I know about this and want the defaults".
			if mappingValue(root, section.key) != nil {
				continue
			}
			key, value, err := sectionNodes(section)
			if err != nil {
				return err
			}
			root.Content = append(root.Content, key, value)
			added = append(added, section.key)
		}
		if len(added) == 0 {
			return nil
		}
		if err := writeYAMLDocument(path, &document); err != nil {
			return err
		}
		// Logging is not configured until after the config loads, so this
		// goes to the default logger for the same reason the retired-field
		// warning does. It is Info rather than Warn: nothing stopped
		// applying, a setting the owner can now see and change appeared.
		slog.Info("added new config settings at their defaults", "path", path, "settings", strings.Join(added, ", "))
		return nil
	})
}

// sectionNodes renders one section as the key/value pair a YAML mapping is
// built from. The comment goes on the key rather than on the value: attached
// to the value it renders indented inside the block it is introducing, which
// reads as a note about the first setting rather than about the section.
func sectionNodes(section defaultedSection) (key, value *yaml.Node, err error) {
	encoded, err := yaml.Marshal(section.value())
	if err != nil {
		return nil, nil, fmt.Errorf("marshal %s defaults: %w", section.key, err)
	}
	var parsed yaml.Node
	if err := yaml.Unmarshal(encoded, &parsed); err != nil {
		return nil, nil, fmt.Errorf("decode %s defaults: %w", section.key, err)
	}
	if len(parsed.Content) == 0 {
		return nil, nil, fmt.Errorf("%s defaults rendered nothing", section.key)
	}
	key = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: section.key, HeadComment: section.comment}
	return key, parsed.Content[0], nil
}
