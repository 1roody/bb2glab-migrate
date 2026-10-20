// Comando bb2gl: migra repositórios do Bitbucket Cloud para o GitLab (gitlab.com).
//
// Este arquivo só trata o Ctrl+C e chama internal/migration, onde está o fluxo:
// verificação (somente leitura) → confirmação → migração → resumo. Veja README.md e docs/.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/1roody/bb2glab-migrate/internal/migration"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ctrl+C antes da migração encerra na hora (nada foi alterado). Durante a migração,
	// termina o repositório atual como falha, mostra o resumo e sai com 130; um segundo
	// Ctrl+C encerra imediatamente.
	var migrating atomic.Bool
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		if !migrating.Load() {
			fmt.Fprintln(os.Stderr, "\nInterrompido pelo usuário. Nada mais foi alterado.")
			os.Exit(130)
		}
		cancel()
		<-sig
		fmt.Fprintln(os.Stderr, "\nInterrompido pelo usuário.")
		os.Exit(130)
	}()

	deps := migration.DefaultDependencies()
	deps.OnMigrate = func() { migrating.Store(true) }
	os.Exit(migration.RunCLI(ctx, os.Args[1:], deps))
}
