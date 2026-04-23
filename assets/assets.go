package assets

import "embed"

//go:embed lambda/*
var LambdaBinaries embed.FS
