#!/bin/sh
set -eu

if [ "$#" -gt 4 ]; then
    printf 'Usage: %s [sources-list [sources.list.d [native-origins.d [origin-files.d]]]]\n' "$0" >&2
    exit 2
fi

sources_list=${1:-/etc/apt/sources.list}
sources_list_dir=${2:-/etc/apt/sources.list.d}
native_origins_dir=${3:-/etc/apport/native-origins.d}
origin_files_dir=${4:-/usr/lib/authd}
authd_ppa_names='authd authd-candidate authd-edge authd-dev'

list_file_authd_ppas() {
    awk -v authd_ppa_names="$authd_ppa_names" '
        function authd_ppa_for_uri(uri, i) {
            for (i = 1; i <= authd_ppa_count; i++) {
                if (uri ~ ("^https?://ppa\\.launchpad(content)?\\.net/ubuntu-enterprise-desktop/" authd_ppas[i] "/ubuntu/?$")) {
                    return authd_ppas[i]
                }
            }
            return ""
        }
        BEGIN {
            authd_ppa_count = split(authd_ppa_names, authd_ppas, /[[:space:]]+/)
        }
        /^[[:space:]]*#/ {
            next
        }
        /^[[:space:]]*deb(-src)?[[:space:]]+/ {
            line = $0
            sub(/\r$/, "", line)
            sub(/^[[:space:]]*deb(-src)?[[:space:]]+/, "", line)
            if (substr(line, 1, 1) == "[") {
                options_end = index(line, "]")
                if (options_end > 0) {
                    line = substr(line, options_end + 1)
                }
            }
            sub(/^[[:space:]]+/, "", line)
            split(line, fields, /[[:space:]]+/)
            ppa = authd_ppa_for_uri(fields[1])
            if (ppa != "") {
                printf "%s ", ppa
            }
        }
    ' "$1"
}

sources_file_authd_ppas() {
    awk -v authd_ppa_names="$authd_ppa_names" '
        function authd_ppa_for_uri(uri, i) {
            for (i = 1; i <= authd_ppa_count; i++) {
                if (uri ~ ("^https?://ppa\\.launchpad(content)?\\.net/ubuntu-enterprise-desktop/" authd_ppas[i] "/ubuntu/?$")) {
                    return authd_ppas[i]
                }
            }
            return ""
        }
        function add_stanza_ppa(ppa) {
            if (index(" " stanza_ppas " ", " " ppa " ") == 0) {
                stanza_ppas = stanza_ppas " " ppa
            }
        }
        function scan_uris(value, uris, count, i, ppa) {
            count = split(value, uris, /[[:space:]]+/)
            for (i = 1; i <= count; i++) {
                ppa = authd_ppa_for_uri(uris[i])
                if (ppa != "") {
                    add_stanza_ppa(ppa)
                }
            }
        }
        function finish_stanza(ppas, count, i) {
            if (enabled) {
                count = split(stanza_ppas, ppas, /[[:space:]]+/)
                for (i = 1; i <= count; i++) {
                    if (ppas[i] != "") {
                        printf "%s ", ppas[i]
                    }
                }
            }
            enabled = 1
            stanza_ppas = ""
            in_uris = 0
        }
        BEGIN {
            RS = ""
            FS = "\n"
            authd_ppa_count = split(authd_ppa_names, authd_ppas, /[[:space:]]+/)
        }
        {
            enabled = 1
            stanza_ppas = ""
            in_uris = 0
            for (i = 1; i <= NF; i++) {
                line = $i
                sub(/\r$/, "", line)
                if (line ~ /^[[:space:]]*$/) {
                    finish_stanza()
                    continue
                }
                if (line ~ /^[[:space:]]*#/) {
                    continue
                }
                if (line ~ /^[[:space:]]/) {
                    if (in_uris) {
                        scan_uris(line)
                    }
                    continue
                }
                in_uris = 0
                field = line
                sub(/:.*/, "", field)
                value = line
                sub(/^[^:]*:[[:space:]]*/, "", value)
                sub(/^[[:space:]]+/, "", value)
                sub(/[[:space:]]+$/, "", value)
                field = tolower(field)
                if (field == "enabled" && tolower(value) == "no") {
                    enabled = 0
                }
                if (field == "uris") {
                    scan_uris(value)
                    in_uris = 1
                }
            }
            finish_stanza()
        }
    ' "$1"
}

authd_ppa_is_enabled() {
    case " $enabled_ppas " in
        *" $1 "*) return 0 ;;
        *) return 1 ;;
    esac
}

enabled_ppas=
if [ -f "$sources_list" ]; then
    enabled_ppas="$enabled_ppas $(list_file_authd_ppas "$sources_list")"
fi

for source in "$sources_list_dir"/*.list; do
    [ -f "$source" ] || continue
    enabled_ppas="$enabled_ppas $(list_file_authd_ppas "$source")"
done

for source in "$sources_list_dir"/*.sources; do
    [ -f "$source" ] || continue
    enabled_ppas="$enabled_ppas $(sources_file_authd_ppas "$source")"
done

remove_native_origin() {
    native_origin_entry=$1
    native_origin_file=$2
    if [ -L "$native_origin_entry" ] &&
       [ "$(readlink "$native_origin_entry")" = "$native_origin_file" ]; then
        rm -- "$native_origin_entry"
    elif [ -f "$native_origin_entry" ] && [ -f "$native_origin_file" ] &&
         cmp -s "$native_origin_entry" "$native_origin_file"; then
        rm -- "$native_origin_entry"
    fi
}

enable_native_origin() {
    native_origin_entry=$1
    native_origin_file=$2
    if [ ! -f "$native_origin_file" ]; then
        printf 'authd: Apport native-origin file is missing: %s\n' "$native_origin_file" >&2
        return 0
    fi

    if [ -L "$native_origin_entry" ]; then
        if [ "$(readlink "$native_origin_entry")" = "$native_origin_file" ]; then
            return 0
        fi
        printf 'authd: not replacing existing Apport native-origin entry: %s\n' "$native_origin_entry" >&2
        return 0
    fi

    if [ -e "$native_origin_entry" ]; then
        if [ -f "$native_origin_entry" ] && cmp -s "$native_origin_entry" "$native_origin_file"; then
            return 0
        fi
        printf 'authd: not replacing existing Apport native-origin entry: %s\n' "$native_origin_entry" >&2
        return 0
    fi

    ln -s -- "$native_origin_file" "$native_origin_entry"
}

for ppa in $authd_ppa_names; do
    if authd_ppa_is_enabled "$ppa" && [ -f "$origin_files_dir/$ppa-native-origins" ]; then
        mkdir -p "$native_origins_dir"
        break
    fi
done

for ppa in $authd_ppa_names; do
    native_origin_entry="$native_origins_dir/$ppa"
    native_origin_file="$origin_files_dir/$ppa-native-origins"

    if authd_ppa_is_enabled "$ppa"; then
        enable_native_origin "$native_origin_entry" "$native_origin_file"
    else
        remove_native_origin "$native_origin_entry" "$native_origin_file"
    fi
done
