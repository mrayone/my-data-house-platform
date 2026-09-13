// Package version centraliza a string de versão dos binários da plataforma.
//
// Na PoC o valor é fixo em "dev"; quando houver pipeline de release, isto vira
// ldflags injetadas no build (ex.: -X internal/platform/version.Version=...).
package version

// Version é a versão corrente do binário. "dev" fora de um build de release.
var Version = "dev"
