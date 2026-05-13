package main

import "embed"

//go:embed web/*
var adminWebEmbedded embed.FS
