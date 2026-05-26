// Package cmd is the emission CLI and HTTP server. The root binary is one
// directory up and just calls [RootCmd].
//
//	@title		emission API
//	@version	1.0
//	@description	Spoof BitTorrent tracker announces to boost your ratio.
//	@BasePath	/
package cmd

//go:generate go tool swag init --dir . --generalInfo doc.go --parseDependency --parseInternal --output ../internal/docs
