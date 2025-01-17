// Package main is the entry point.
package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/ubuntu/authd-oidc-brokers/cmd/authd-oidc/daemon"
	"github.com/ubuntu/authd-oidc-brokers/internal/consts"
	"github.com/ubuntu/authd-oidc-brokers/po"
	"github.com/ubuntu/authd/log"
	"github.com/ubuntu/go-i18n"
)

//FIXME go:generate go run ../generate_completion_documentation.go completion ../../generated
//FIXME go:generate go run ../generate_completion_documentation.go update-readme
//FIXME go:generate go run ../generate_completion_documentation.go update-doc-cli-ref

func main() {
	i18n.InitI18nDomain(consts.TEXTDOMAIN, po.Files)
	a := daemon.New(filepath.Base(os.Args[0]))
	os.Exit(run(a))
}

type app interface {
	Run() error
	UsageError() bool
	Hup() bool
	Quit()
}

func run(a app) int {
	defer installSignalHandler(a)()

	if err := a.Run(); err != nil {
		log.Error(context.Background(), err.Error())

		if a.UsageError() {
			return 2
		}
		return 1
	}

	return 0
}

func installSignalHandler(a app) func() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			switch v, ok := <-c; v {
			case syscall.SIGINT, syscall.SIGTERM:
				a.Quit()
				return
			case syscall.SIGHUP:
				if a.Hup() {
					a.Quit()
					return
				}
			default:
				// channel was closed: we exited
				if !ok {
					return
				}
			}
		}
	}()

	return func() {
		signal.Stop(c)
		close(c)
		wg.Wait()
	}
}
