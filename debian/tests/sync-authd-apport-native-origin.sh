#!/bin/sh
set -eu

helper=${1:-/usr/lib/authd/sync-authd-apport-native-origin.sh}
origin_files_dir=${2:-/usr/lib/authd}
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' 0

sources_list="$tmpdir/sources.list"
sources_list_dir="$tmpdir/sources.list.d"
native_origins_dir="$tmpdir/native-origins.d"
mkdir -p "$sources_list_dir" "$native_origins_dir"

assert_enabled() {
    ppa=$1
    native_origin_entry="$native_origins_dir/$ppa"
    native_origin_file="$origin_files_dir/$ppa-native-origins"
    [ -L "$native_origin_entry" ]
    [ "$(readlink "$native_origin_entry")" = "$native_origin_file" ]
    [ "$(cat "$native_origin_file")" = "LP-PPA-ubuntu-enterprise-desktop-$ppa" ]
}

assert_disabled() {
    ppa=$1
    native_origin_entry="$native_origins_dir/$ppa"
    [ ! -e "$native_origin_entry" ]
    [ ! -L "$native_origin_entry" ]
}

assert_only_enabled() {
    expected=${1:-}
    for ppa in authd authd-candidate authd-edge authd-dev; do
        if [ "$ppa" = "$expected" ]; then
            assert_enabled "$ppa"
        else
            assert_disabled "$ppa"
        fi
    done
}

sync_origin() {
    "$helper" "$sources_list" "$sources_list_dir" "$native_origins_dir" "$origin_files_dir"
}

sync_origin
assert_only_enabled

cat > "$sources_list_dir/authd.sources" <<'EOF'
Types: deb
URIs: https://archive.ubuntu.com/ubuntu/
  https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu/
Suites: noble
Components: main
EOF
sync_origin
assert_only_enabled authd
sync_origin
assert_only_enabled authd
printf 'Types: deb\r\nURIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu/\r\nSuites: noble\r\nComponents: main\r\n\r\nTypes: deb\r\nURIs: https://archive.ubuntu.com/ubuntu/\r\nSuites: noble\r\nComponents: main\r\nEnabled: no\r\n' > "$sources_list_dir/authd.sources"
sync_origin
assert_only_enabled authd

printf 'Types: deb\r\nURIs: https://archive.ubuntu.com/ubuntu/\r\nSuites: noble\r\nComponents: main\r\nEnabled: no\r\n\r\nTypes: deb\r\nURIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu/\r\nSuites: noble\r\nComponents: main\r\n' > "$sources_list_dir/authd.sources"
sync_origin
assert_only_enabled authd

cat > "$sources_list_dir/candidate.sources" <<'EOF'
Types: deb
URIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd-candidate/ubuntu/
Suites: noble
Components: main
EOF
sync_origin
assert_enabled authd
assert_enabled authd-candidate
assert_disabled authd-edge
assert_disabled authd-dev

rm "$sources_list_dir/candidate.sources"
sync_origin
assert_only_enabled authd

cat > "$sources_list_dir/authd.sources" <<'EOF'
Types: deb
URIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu/
Suites: noble
Components: main
Enabled: no
EOF
sync_origin
assert_only_enabled

cat > "$sources_list_dir/authd.sources" <<'EOF'
Types: deb
URIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd-edge/ubuntu/
Suites: noble
Components: main
EOF
sync_origin
assert_only_enabled authd-edge

rm "$sources_list_dir/authd.sources"
cat > "$sources_list_dir/candidate.sources" <<'EOF'
Types: deb
URIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd-candidate/ubuntu/
Suites: noble
Components: main
EOF
sync_origin
assert_only_enabled authd-candidate

rm "$sources_list_dir/candidate.sources"
cat > "$sources_list_dir/authd.list" <<'EOF'
deb [arch=amd64] http://ppa.launchpad.net/ubuntu-enterprise-desktop/authd-edge/ubuntu/ noble main
EOF
sync_origin
assert_only_enabled authd-edge

rm "$sources_list_dir/authd.list"
cat > "$sources_list" <<'EOF'
deb-src https://ppa.launchpad.net/ubuntu-enterprise-desktop/authd-dev/ubuntu noble main
EOF
sync_origin
assert_only_enabled authd-dev

: > "$sources_list"
sync_origin
assert_only_enabled

cp "$origin_files_dir/authd-native-origins" "$native_origins_dir/authd"
sync_origin
assert_only_enabled

cat > "$sources_list_dir/authd.sources" <<'EOF'
Types: deb
URIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu/
Suites: noble
Components: main
EOF
printf 'custom origin\n' > "$native_origins_dir/authd"
sync_origin 2>"$tmpdir/error"
[ "$(cat "$native_origins_dir/authd")" = 'custom origin' ]
grep -q 'not replacing existing Apport native-origin entry' "$tmpdir/error"

rm "$native_origins_dir/authd"
cat > "$sources_list_dir/authd.sources" <<'EOF'
Types: deb
URIs: https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd/ubuntu/
  https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd-candidate/ubuntu/
  https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd-edge/ubuntu/
  https://ppa.launchpadcontent.net/ubuntu-enterprise-desktop/authd-dev/ubuntu/
Suites: noble
Components: main
EOF
sync_origin
for ppa in authd authd-candidate authd-edge authd-dev; do
    assert_enabled "$ppa"
done

missing_native_origins_dir="$tmpdir/missing-native-origins.d"
missing_origin_files_dir="$tmpdir/missing-origin-files"
mkdir -p "$missing_native_origins_dir" "$missing_origin_files_dir"
"$helper" "$sources_list" "$sources_list_dir" "$missing_native_origins_dir" "$missing_origin_files_dir" 2>"$tmpdir/missing-origin-error"
grep -q 'Apport native-origin file is missing' "$tmpdir/missing-origin-error"
for ppa in authd authd-candidate authd-edge authd-dev; do
    [ ! -e "$missing_native_origins_dir/$ppa" ]
    [ ! -L "$missing_native_origins_dir/$ppa" ]
done
