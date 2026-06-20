// Package personality loads Cardinal's signature personality from
// the embedded cardinal.md file. This is the core identity of the agent.
package personality

import _ "embed"

//go:embed cardinal.md
var cardinalMD string

// Load returns Cardinal's personality text from cardinal.md.
func Load() string {
	return cardinalMD
}
