package daemon

import (
	"fmt"

	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	"github.com/spf13/cobra"
)

func (a *App) installVersion() {
	cmd := &cobra.Command{
		Use:                                                     "version",
		Short:/*i18n.G(*/ "Returns version of daemon and exits", /*)*/
		Args:                                                    cobra.NoArgs,
		RunE:                                                    func(cmd *cobra.Command, args []string) error { return a.getVersion() },
	}
	a.rootCmd.AddCommand(cmd)
}

// getVersion returns the current service version.
func (a *App) getVersion() (err error) {
	fmt.Printf( /*i18n.G(*/ "%s\t%s" /*)*/ +"\n", a.name, consts.Version)
	return nil
}
