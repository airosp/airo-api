// Package migrations carrega o DDL para dentro do binário.
//
// Embutido e não lido do disco: o contentor de produção não tem o repositório,
// e uma migração que depende de um ficheiro ao lado do binário falha no pior
// momento possível — no arranque, depois do deploy.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
