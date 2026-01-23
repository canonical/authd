// Package main is the entry point.
package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/canonical/authd/cmd/authd/daemon"
	"github.com/canonical/authd/log"
)

//FIXME go:generate go run ../generate_completion_documentation.go completion ../../generated
//FIXME go:generate go run ../generate_completion_documentation.go update-readme
//FIXME go:generate go run ../generate_completion_documentation.go update-doc-cli-ref

func main() {
	//i18n.InitI18nDomain(common.TEXTDOMAIN)
	a := daemon.New()
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
		log.Error(context.Background(), err)

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
				log.Infof(context.Background(), "Received signal %d (%s), exiting...", v, v.String())
				a.Quit()
				return
			case syscall.SIGHUP:
				if a.Hup() {
					log.Info(context.Background(), "Received SIGHUP, exiting...")
					a.Quit()
					return
				}
				log.Info(context.Background(), "Received SIGHUP, but nothing to do")
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
