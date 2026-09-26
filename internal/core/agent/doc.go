// Package agent is the model-and-tools loop at the center of every turn: send
// the conversation to the model, run the tools it asks for, feed the results
// back, and repeat until it answers.
//
// loop.go is the loop and its event stream; prompt.go builds the system
// instructions (runtime policy, capability manifest, skills, time); and
// compaction.go keeps a long turn inside the model's context window.
package agent
