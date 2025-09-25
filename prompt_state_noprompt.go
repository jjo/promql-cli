//go:build !prompt

package main

// inGoPromptSession is only defined in prompt builds; provide a stub for non-prompt builds.
var inGoPromptSession bool = false
