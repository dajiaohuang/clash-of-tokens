package api

import _ "embed"

//go:embed control.html
var dashboard string

//go:embed control.js
var controlJS string

//go:embed control.css
var controlCSS string

//go:embed chat.html
var chatPage string
