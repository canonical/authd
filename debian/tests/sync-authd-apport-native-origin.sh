#!/bin/sh
set -eu

helper=${1:-/usr/lib/authd/sync-authd-apport-native-origin.sh}
origin_file=${2:-/usr/lib/authd/authd-native-origins}
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' 0

sources_list="$tmpdir/sources.list"
sources_list_dir="$tmpdir/sources.list.d"
native_origins_dir="$tmpdir/native-origins.d"
native_origin_entry="$native_origins_dir/authd"
mkdir -p "$sources_list_dir" "$native_origins_dir"

sync_origin() {
    "$helper" "$sources_list" "$sources_list_dir" "$native_origins_dir" "$origin_file"
}

assert_enabled() {
    [ -L "$native_origin_entry" ]
    [ "$(readlink "$native_origin_entry")" = "$origin_file" ]
}

assert_disabled() {
    [ ! -e "$native_origin_entry" ]
    [ ! -L "$native_origin_entry" ]
}

sync_origin
assert_disabled

cat > "$sources_list_dir/authd.sources" <<'EOF'
Types: deb
URIs: https://archive.ubuntu.com/ubuntu/
  https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu/
Suites: noble
Components: main
EOF
sync_origin
assert_enabled
sync_origin
assert_enabled
printf 'Types: deb\r\nURIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu/\r\nSuites: noble\r\nComponents: main\r\n\r\nTypes: deb\r\nURIs: https://archive.ubuntu.com/ubuntu/\r\nSuites: noble\r\nComponents: main\r\nEnabled: no\r\n' > "$sources_list_dir/authd.sources"
sync_origin
assert_enabled

printf 'Types: deb\r\nURIs: https://archive.ubuntu.com/ubuntu/\r\nSuites: noble\r\nComponents: main\r\nEnabled: no\r\n\r\nTypes: deb\r\nURIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu/\r\nSuites: noble\r\nComponents: main\r\n' > "$sources_list_dir/authd.sources"
sync_origin
assert_enabled

cat > "$sources_list_dir/authd.sources" <<'EOF'
Types: deb
URIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu/
Suites: noble
Components: main
Enabled: no
EOF
sync_origin
assert_disabled

cat > "$sources_list_dir/authd.sources" <<'EOF'
Types: deb
URIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd-edge/ubuntu/
Suites: noble
Components: main
EOF
sync_origin
assert_disabled

rm "$sources_list_dir/authd.sources"
cat > "$sources_list_dir/authd.list" <<'EOF'
deb [arch=amd64] http://ppa.launchpad.net/ubuntu-enterprise-desktop/authd/ubuntu/ noble main
EOF
sync_origin
assert_enabled

rm "$sources_list_dir/authd.list"
cat > "$sources_list" <<'EOF'
deb https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu noble main
EOF
sync_origin
assert_enabled

: > "$sources_list"
sync_origin
assert_disabled

cp "$origin_file" "$native_origin_entry"
sync_origin
assert_disabled

cat > "$sources_list_dir/authd.sources" <<'EOF'
Types: deb
URIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu/
Suites: noble
Components: main
EOF
printf 'custom origin\n' > "$native_origin_entry"
sync_origin 2>"$tmpdir/error"
[ "$(cat "$native_origin_entry")" = 'custom origin' ]
grep -q 'not replacing existing Apport native-origin entry' "$tmpdir/error"
