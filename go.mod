module github.com/canonical/authd

go 1.25.0

toolchain go1.25.7

require (
	github.com/charmbracelet/bubbles v0.20.0
	github.com/charmbracelet/bubbletea v1.3.4
	github.com/charmbracelet/lipgloss v1.1.0
	github.com/charmbracelet/x/term v0.2.2
	github.com/coreos/go-systemd/v22 v22.7.0
	github.com/godbus/dbus/v5 v5.2.2
	github.com/google/uuid v1.6.0
	github.com/mattn/go-sqlite3 v1.14.33
	github.com/msteinert/pam/v2 v2.0.0
	github.com/muesli/termenv v0.16.0
	github.com/otiai10/copy v1.14.1
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	github.com/spf13/cobra v1.10.2
	github.com/spf13/viper v1.21.0
	github.com/stretchr/testify v1.11.1
	go.etcd.io/bbolt v1.4.3
	golang.org/x/exp v0.0.0-20230905200255-921286631fa9
	golang.org/x/sys v0.40.0
	golang.org/x/term v0.39.0
	google.golang.org/grpc v1.78.0
	google.golang.org/protobuf v1.36.11
	gopkg.in/ini.v1 v1.67.1
	gopkg.in/yaml.v3 v3.0.1
	gorbe.io/go/osrelease v0.3.0
)

require (
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/colorprofile v0.2.3-0.20250311203215-f60798e515dc // indirect
	github.com/charmbracelet/x/ansi v0.8.0 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.13-0.20250311204145-2c3ea96c31dd // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/otiai10/mint v1.6.3 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sagikazarmark/locafero v0.11.0 // indirect
	github.com/sahilm/fuzzy v0.1.1 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sync v0.18.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251029180050-ab9386a59fda // indirect
)

// FIXME: Use released version once we have one!
// The branch below includes changes from this upstream PR:
// - https://github.com/msteinert/pam/pull/13
replace github.com/msteinert/pam/v2 => github.com/3v1n0/go-pam/v2 v2.0.0-20240321054421-f19903865176
