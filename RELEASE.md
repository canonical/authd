# Releasing authd and the brokers

## Prepare release branch

1. Ensure you’re on the main branch.
2. Create the changelog entry

    ```shell
    # Ensure your DEBEMAIL is set, for example:
    export DEBEMAIL="user@example.com"
    gbp dch --multimaint-merge --local "~pre" HEAD
    ```

   - This will generate a very verbose changelog with all the commits since last version, so clean it up so that only relevant changes are included.
   - Note that it is good practice to always include all the changes
     that are affecting debian/\* files.
   - To figure out the changes since the last release, it has proven useful to go through the merge commits since then:

       ```shell
       PREVIOUS_TAG=$(git tag | tail -n1)
       echo $PREVIOUS_TAG
       PREVIOUS_VERSION=${PREVIOUS_TAG#v}
       git log ${PREVIOUS_TAG}..
       ```

     Search for "Merge: " to go through the merge commits.

   - Also add the updated Go and Rust dependencies. You can use the [updated-go-dependencies](https://github.com/adombeck/authd-scripts/blob/main/updated-go-dependencies) and [updated-rust-dependencies](https://github.com/adombeck/authd-scripts/blob/main/updated-rust-dependencies) scripts for that.

   - Based on the changes, decide which version to use for the next release (increase of major, minor or patch version) and set it in the changelog entry. Keep the `~preX` suffix.
   - Keep the target archive as UNRELEASED for now.

3. Create a release branch

    ```shell
    RELEASE_BRANCH="release-$(dpkg-parsechangelog -SVersion | tr '~' '-')"
    git checkout -b "${RELEASE_BRANCH}"
    ```

4. Commit the changelog entry

    ```shell
    git commit -m "Add changelog entry for $(dpkg-parsechangelog -SVersion)" debian/changelog
    ```

5. Push and open a PR


## Download the source packages from the GitHub CI

1. Wait until the source packages from the "Build Debian package" CI jobs are available. This should take 2-3 minutes. Note that you don’t have to wait for the jobs to complete, the source package artifacts are available for download before that:

    ```shell
    RUN=$(gh run list --workflow "debian.yaml" --branch "${RELEASE_BRANCH}" --limit=1 --json databaseId --jq '.[] | .databaseId')
    [ -n "${RUN}" ] &&  watch -n 10 -x gh api /repos/ubuntu/authd/actions/runs/$RUN/artifacts \
      --jq '[.artifacts[] | select(.name | endswith("debian-source"))] | length'
    ```

2. Download the source packages:

    ```shell
    RUN=$(gh run list --workflow "debian.yaml" --branch "${RELEASE_BRANCH}" --json databaseId --jq '.[] | .databaseId' | head -n1)
    [ -n "${RUN}" ] && gh run download "${RUN}" -p "*debian-source" --dir ~/Downloads
    ```

3. Extract the artifacts into a temporary directory:

    ```shell
    cd ~/Downloads
    SOURCE_PKGS=~/tmp/authd-source
    rm -rf "${SOURCE_PKGS}"
    mkdir -p "${SOURCE_PKGS}"
    # This handles manually downloaded artifacts, which are downloaded as a zip archive
    test -n "$(find . -maxdepth 1 -name '*-debian-source.zip' -print -quit)" && for f in "authd_${VERSION}"*-debian-source.zip; do
        unzip -d "${SOURCE_PKGS}" "$f"
    done
    # The `gh run download` command stores the artifacts in a directory instead of a zip archive
    for d in "authd_${VERSION}"*-debian-source; do
        cp "$d"/* "${SOURCE_PKGS}"
    done
    ```

## Rebuild the source packages

The GitHub CI adds a changelog entry which we don’t want to be included in the release.

1. Extract the source packages:

    ```shell
    cd "${SOURCE_PKGS}"
    for f in "authd_${VERSION}"*.tar.*; do
        d="${f%.tar*}"
        rm -rf "$d"
        mkdir "$d"
        tar xaf "$f" -C "$d"
    done
    ```

2. Edit the last changelog entry:
   * Replace the `~pre1` suffix with `~ubuntu24.04~pre1` (or `~ubuntu25.04~pre1` etc.)
   * Remove the "Github build…" bullet point
   * Replace the `@users.noreply.github.com` email with your DEBEMAIL

       ```shell
       dirs -c
       for d in */authd; do
           pushd "$d"
       dch -r ''
       tmpfile=$(mktemp)
       sed \
         -e '1,/^ -- /{ /^  \[ .* \]$/d; }' \
         -e '1,/^ -- /{ /^\s*\* Github build\./d; }' \
         -e '1,/^ -- /s/\(authd (\)\([0-9]\+\.[0-9]\+\.[0-9]\+\)~pre\([0-9]\+\)+git[^~]\+~\(\([0-9]\+\.[0-9]\+\)\.[0-9]\+\)\().\)/\1\2~ubuntu\4~pre\3\6/' \
         debian/changelog \
         | awk '/^$/ { blank_run++; if (blank_run == 1) print; next } { blank_run=0; print }' \
      >    "${tmpfile}" && mv "${tmpfile}" debian/changelog
           popd
       done
       ```

3. Rebuild the source packages:

    ```shell
    for d in */authd; do
        pushd "$d"
        PREVIOUS_VERSION=$(dpkg-parsechangelog --count=2 --reverse -SVersion)
        dpkg-buildpackage --build=source --no-check-builddeps --no-sign -v"${PREVIOUS_VERSION}"
        popd
    done
    ```

4. Sign the source packages (we do that separately to avoid the first command to prompt for a gpg password after it built the first package, which we often missed)

    ```shell
    for f in authd*/authd_*_source.changes; do
        debsign -S --no-re-sign "$f"
    done
    ```

## Build the source package (obsolete if you download the source package from the GitHub CI)

Build the source package (and ensure that there are no lintian issues on the source package):

```shell
gbp buildpackage -S --git-ignore-new --git-export-dir=/tmp/authd-build
```

## Build the binary package (obsolete if you download the binary package from the GitHub CI)

Note that this should be performed for all the supported versions (noble and plucky at the moment):

1. If you didn't create an sbuild schroot before:

    ```shell
    mk-sbuild plucky --distro ubuntu
    ```

2. Build the binary package to ensure it builds and that there are no lintian issues to address on the binary package:

    ```shell
    sbuild -A -v --build-dep-resolver=aptitude -d plucky-amd64 \
      "$(ls -t1 /tmp/authd-build/authd_*.dsc | head -n1)"
    ```

## Push the .changes file to the edge PPA

1. Push the files:

    ```shell
    for f in authd*/authd_*_source.changes; do
        dput ppa:ubuntu-enterprise-desktop/authd-edge "$f"
    done
    ```

2. Check that the builds are scheduled (usually takes \~2 minutes to show up): [https://launchpad.net/\~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+builds?build\_text=\&build\_state=all](https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+builds?build_text=&build_state=all)

3. Wait for the builds to complete (should take around 15 minutes)

4. Wait for the packages to be published (usually takes \~1h, but can take up to several hours): [https://launchpad.net/\~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+packages?field.name\_filter=\&field.status\_filter=\&field.series\_filter=](https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+packages?field.name_filter=&field.status_filter=&field.series_filter=)

## Copy package from edge PPA to candidate PPA

1. Go to [https://launchpad.net/\~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+packages](https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+packages)
2. Click "Copy packages" in the top right
3. Select the authd packages for all currently supported Ubuntu versions
4. Destination PPA: authd candidate
5. Destination series: The the same series
6. Copy options: Copy existing binaries

## Copy snap release from edge to candidate channel

1. Go to [https://snapcraft.io/authd-oidc/releases](https://snapcraft.io/authd-oidc/releases)
2. For all architectures, drag and drop the release from the edge channel to the candidate channel
3. Click "Review changes" in the top right and then "Save"
4. Repeat the same for:
   1. [https://snapcraft.io/authd-msentraid/releases](https://snapcraft.io/authd-msentraid/releases)
   2. [https://snapcraft.io/authd-google/releases](https://snapcraft.io/authd-google/releases)

## Install authd in a VM

1. Clean up previous installs

    ```shell
    sudo apt purge -y authd
    sudo rm -rf /var/lib/authd /var/cache/authd /home/*@*

    sudo apt install -y ppa-purge
    sudo ppa-purge ppa:ubuntu-enterprise-desktop/authd
    sudo ppa-purge ppa:ubuntu-enterprise-desktop/authd-edge
    ```

2. Install Packages from the edge PPA

    ```shell
    sudo add-apt-repository ppa:ubuntu-enterprise-desktop/authd-edge
    sudo apt update -y
    sudo apt dist-upgrade -y
    sudo apt install -y authd
    ```

3. Install the new .deb (this should be not required if the deb from the edge PPA is already synchronized and installed in the previous step)

    ```shell
    sudo dpkg -i $(ls -t ~/tmp/authd_*.deb | head -n1)
    ```

## Build the authd-msentraid snap

```shell
git checkout msentraid
git pull
snapcraft pack
```

## Build the authd-google snap

```shell
git checkout google
git pull
snapcraft pack
```

## Install the snaps

1. Clean up previous installs:

    ```shell
    sudo snap remove authd-msentraid authd-google
    ```

2. Install the new snaps:

    ```shell
    sudo snap install --dangerous "$(ls -t ~/projects/authd-oidc-brokers/authd-msentraid*.snap | head -n1)"
    sudo snap install --dangerous "$(ls -t ~/projects/authd-oidc-brokers/authd-google*.snap | head -n1)"
    ```

3. Configure the msentraid broker. You can find the issuer ID and client ID in
   the [Bitwarden vault](https://vault.bitwarden.com/#/vault?collectionId=b390a2b0-c455-452e-82b5-b3e100cfbbcd&itemId=723fe763-416a-48f6-82e2-b3da00ccd6b7&action=view).

    ```shell
    # ubudev1.onmicrosoft.com
    ISSUER_ID=<issuer ID>
    CLIENT_ID=<client ID>
    sudo sed -i -e "s/<ISSUER_ID>/${ISSUER_ID}/g" -e "s/<CLIENT_ID>/${CLIENT_ID}/g" /var/snap/authd-msentraid/current/broker.conf
    ```

4. Configure the google broker. You can find the client ID and client secret in
   the [Bitwarden vault](https://vault.bitwarden.com/#/vault?collectionId=b390a2b0-c455-452e-82b5-b3e100cfbbcd&itemId=63fe96be-1eb4-4bc1-bb2b-b3da00cc253f&action=view).

    ```shell
    CLIENT_SECRET=<client secret>
    CLIENT_ID=<client ID>
    sudo sed -i -e "s/<CLIENT_SECRET>/${CLIENT_SECRET}/g" -e "s/<CLIENT_ID>/${CLIENT_ID}/g" /var/snap/authd-google/current/broker.conf
    ```

5. Restart the brokers:

    ```shell
    sudo snap restart authd-msentraid authd-google
    sudo journalctl -u snap.authd-msentraid.authd-msentraid.service -u snap.authd-google.authd-google.service -e -n10 -f
    ```

## Do the manual tests

Create a new tab in the [spreadsheet](https://docs.google.com/spreadsheets/d/1FV9r-e9M_Hm_Se2FAaVGHxnsfd-omsRozW43G0qeI8g/edit?gid=1716960291#gid=1716960291) and go through the manual tests.

If you find and fix any bugs:

1. Amend the changelog to increase the number in the `~preX` suffix:

```shell
OLD_PRERELEASE_VERSION=$(dpkg-parsechangelog -SVersion)
debchange --local "~pre" ""
git commit -m "Change $OLD_PRERELEASE_VERSION to $(dpkg-parsechangelog -SVersion)"
```

1. Push the commit

2. Wait for the CI jobs to finish

3. Redo the relevant tests, depending on the contents of the changes:
   1. Either with the CI artifacts:
      1. Extract the artifacts into a temporary directory:

         ```shell
         cd ~/Downloads
         BINARY_PKGS=~/tmp/authd-packages
         rm -rf "${BINARY_PKGS}"
         mkdir -p "${BINARY_PKGS}"
         for f in "authd_${VERSION}"*-debian-packages.zip; do
             unzip -d "${BINARY_PKGS}" "$f"
         done
         ```
      2. Install the Debian package via `sudo apt install ./authd_*.deb` while the edge PPA is installed, to ensure that the dependencies like `gnome-shell` can be installed
   2. Or, to test with a package closer to the one that will be released, follow the "[Rebuild the source package](#rebuild-the-source-packages)" and "[Push the .changes file to the edge PPA](#push-the-changes-file-to-the-edge-ppa)" sections again.

## Finalize the release branch

1. Amend the changelog to remove the `~preX` suffix

2. Wait for the PR to be approved.

3. Create a separate commit which updates the changelog to target the next Ubuntu release instead of UNRELEASED:

    ```shell
    debchange -r "" --distribution resolute # Replace it with the actual release name
    git commit -m "Upload $(dpkg-parsechangelog -SVersion) to $(dpkg-parsechangelog -SDistribution)" debian/changelog
    ```

4. Create a git tag:

    ```shell
    gbp buildpackage --git-debian-branch="release-$(dpkg-parsechangelog -SVersion)" --git-tag-only --git-ignore-new --git-sign-tags || git reset --hard HEAD^
    ```

5. Do a dry-run push:

    ```shell
    gbp push --dry-run --debian-branch="release-$(dpkg-parsechangelog -SVersion)"
    ```

6. Review the dry run, make sure that it's on the correct commit.

7. Generate the final debian source files in a clean git repo:

    ```shell
    GIT_DIR=$PWD
    TMP_GIT_DIR=~/tmp/authd
    rm -rf "$TMP_GIT_DIR"
    git clone "$GIT_DIR" "$TMP_GIT_DIR"
    cd "$TMP_GIT_DIR"
    git checkout "release-$VERSION"
    gbp buildpackage -S --git-debian-branch="release-$VERSION" --git-ignore-new -d
    ```

8. Push the `.changes` file to the edge PPA:

    ```shell
    dput ppa:ubuntu-enterprise-desktop/authd-edge "../authd_${VERSION}_source.changes"
    ```

9. Push the commits and tag to the release branch:

    ```shell
    cd "${GIT_DIR}"
    gbp push --debian-branch="release-$VERSION"
    ```

10. Merge the release branch to main

## Tag the broker branches with the version

1. Find the commit IDs which the [candidate release](https://snapcraft.io/authd-oidc/releases) of authd-oidc was built from.
   The first commit ID in the actual commit the release was built from, the second commit ID is the merge-base with the main branch.
   <br><br>
   ![image-find-commit-ids](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAA+EAAAD8CAYAAAAYGaU+AAAgAElEQVR4XuydBbgV1deHN6GIBQYGFnZii93dgYXdqBiY2I3djZ3YhY3dit2NDQaiiAgY+Pnu/7Put+/cmTlzzj33njnDbz+Pj3rPOTN73l3rt9bae9r8+19xKiIgAiIgAmURGD58uJt55pnL+o2+LAIiIAIiIAIiIAIiIAJt4kT4sGHDREYEREAEREAEREAEREAEREAEREAERKDKBGJFeJXvocuJgAiIQOEIvP76627BBRcs3HPpgURABESgmgQmn3zyal5O1xIBERCBQhCQCC9EM+ohREAEWpuARHhrE9f9REAE6pGARHg9tprqLAIi0NIEJMJbmrCuLwIiUEgCEuGFbFY9lAiIQJUJSIRXGaguJwIiUAgCEuGFaEY9hAiIQGsTkAhvbeK6nwiIQD0SkAivx1ZTnUVABFqagER4SxPW9UVABApJQCK8kM2qhxIBEagyAYnwKgPV5URABApBQCK8EM2ohxABEWhtAhLhrU1c9xMBEahHAhLh9dhqqrMIiEBLE5AIb2nCur4IiEAhCUiEF7JZ9VAiIAJVJiARXmWgupwIiEAhCEiEF6IZ9RAiIAKtTUAivLWJ634iIAL1SEAivB5bTXUWARFoaQIS4S1NWNcXAREoJAGJ8EI2qx5KBESgygQkwqsMVJcTAREoBAGJ8EI0ox5CBESgtQlIhLc2cd1PBESgHglIhNdjq6nOIiACLU1AIrylCev6IiAChSQgEV7IZtVDiYAIVJmARHiVgepyIiAChSAgEV6IZtRDiIAItDYBifDWJq77iYAI1CMBifB6bDXVWQREoKUJSIS3MOF///3X/fTTT26GGWYo604//PCDm3HGGcv6jb4sAiLQegQkwluPte4kAiJQvwQkwuu37VRzERCBliOQSYQ///zzbsiQIW7HHXd0Xbp08bX5/vvv3cCBA91yyy3nVlhhhZarYTOuPGzYMNetWzf3zTff1EzQXnLJJe6WW25xMKRsuummrmvXru6yyy5LfLL333/fLbLIIu6vv/5y7du3bwYB/VQERKClCEiEtxRZXVcERKBIBMoR4ePGjXOTTTZZkR5fzyICIiACsQQyifD999/f/fnnn15s77zzzv5C11xzjXvllVdchw4d3IUXXlgx3pdeesn17dvXvfvuu27++ed35513nltttdUqvl74wwsuuMANGjTIPfHEE1W5XiUXufjii92tt97aIMIPOeQQ7xA4/PDDEy/33nvvue7du2cW4V999ZU77LDD3O23315JFfUbERCBCgjUQoSPHDnSMa/dc8893rnYuXNnt+GGG7oTTzzRTTfddE2eYvjw4W699dZzn332mRszZkwFT6mfiIAIiEDzCGQV4dgyN954o9tqq628PdicMmrUKPfkk0+6Tz75xNtSBJDWXHNNN8888zS67OjRo92DDz7ohg4d6jp27OhWXHFF16NHj+bcWr8VAREQgUwEMolwRDbR2V133dVHvikvvPCCu+GGG9yiiy7q+vTpk+lm0S998cUXbvHFF3f77ruvn3QxLBHhb7/9tpt77rkrumb4I3Ma9O7du9nXqvQCURGe5TrlivCXX37ZrbTSSu7vv//Ocnl9RwREoAoEaiHCcShusskmrk2bNt6oZKsLW14wHAcPHtzwVPyN+RSn348//uj/LhFehUbXJURABMomkFWEY8PcdNNNDtuwV69eboEFFij7XvYDtvS99tpr3sZkvnzzzTfdG2+84e3Vaaed1n+NeXLAgAFuqqmm8sGfESNGuAceeMBnLJKNqCICIiACLUkgkwhvqQoQYUdwP/vssw23wFM533zzpaZrZ6nP119/7T2e3333XUMKfZbfVfs7EuHVJqrriUA+CGQR4USif/vtt5JRnX/++ceRFYQzrVS5/PLL3TbbbOOmmWYaRx1WXXVVb0wyl1qUZ5111vGO0rBIhJciq89FQARagkBWEW73JsDz+eef++BMNcUwAaVll13W/0P58MMPvbPy4IMPbkiBf+aZZ3xm5n777dcSKHRNERABEWggUFMRjtjGK3nggQc2VIg91GeddZb78ssvmzQTaUV4NR9++GFveFLeeustt8oqq3gvZxg95xqPPfZYo+gQ/3/ccce5d955x6cdbbzxxu7aa691Y8eOdSuvvLJP2STtfqmllvJOAJv8iTbtvffe3qvKXvhJJ53UZwQgsMOUKQzh0047zV155ZU+QrXwwgv7A9l++eWXhnT0bbfd1s0666zu7LPPbni+e++916eTfvzxx26OOebw177uuusa0tFL1Y9I+PLLL9+I13PPPecNeup07rnn+roSEVt66aX99oHFFltMw0AERKAZBLKI8A022MAbeo8//nhi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No0qJgIikCMCEuE5agxVRQREIDcEJMJz0xSqiAiIQD0RkAivp9ZSXUVABGpFQCK8VuR1XxEQgTwTkAjPSev89ddfbtSoUW766afPSY3+vxrDhw93M888s/8D/z3TTDO5Nm3a5K6eqpAItCYBifDWpB1/rzFjxrh//vnHTT311LWvTFCDcD6fMGGC++mnn9yMM86Yqzp+8sknbt5559VcnqtWKWZlJMJr366///67m3LKKWtfkUgNmCuZIzt06OD+/vtv/89kk02Wm3qOHz/eUcc8sssNJFWkYgIS4RWjq+4P33//fffiiy+6Pffcs7oXbubVmHyOOuood9ZZZ/krHXLIIe700093k0wySTOvXP7Pzz//fLf11lu7rl27lv9j/UIEqkxAIrzKQCu43KBBg9y0007rVlpppQp+3XI/+eCDD9wrr7zidt11V++4HDhwoJ87W7t8/vnn7umnn3a77757k1sffvjhfi5v27Zta1dL95vICEiE17bBEbmPP/64W2eddWpbkZi7f/TRR65jx45ujjnmcF9++aUbN26cW2CBBVq9np9++qm3a7t169bo3l988YX7888/3fzzz9/qddINi0+gpiIcz9x1113nvv76ax/J2Hnnnd1ss83WhPq///7r7rzzTvfaa6+5du3auU033dQtu+yysa3z1VdfuZtuusn9+uuvflBjBE0xxRQt2pK//PKLe+ihhxyRhYUWWshts8027scff2wQrnbzP/74w+2xxx5uiSWWaFKfOBEed92sD8JEdvPNN7vPPvvMwW+55ZZzm222WaOfYxyed9557phjjkmMJJUrwi+55BL37bff+nai0FbLLLNMYrWp5/XXX+9oNzyO8803n9txxx1d3KJdSoS/8cYb7oEHHnDHHXdco/s9+OCD7oUXXvDe1jnnnNPttNNOftKPlnPOOcctuuiibu211270EfwwVtdYY43Efpe1XWhnhANZD9Rhyy23dAsvvHCjn2O8P/vss2706NFuv/32czPMMEPWy+t7rUigViKc+fLWW2/1c9zss8/ux0vcHMf8ylz4zTff+PG9/fbbu1lnnTWW0JAhQ/wcxnhfeuml/VzR0tkuGDcYht9//71bf/31/X2px7333tuojowDxl/cM8aJ8LjrZu0W77zzjh+fGF3GzLKAuAZzwd133+05bbvttomXLVeE9+vXr9Gc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UFcEJMLrqrlUWREQgRoRkAivEXjdVgREINcEJMJz3TyqnAiIQF4JIMLXXnvtvFZP9RIBERCBXBAYOXJkLuqhSoiACIhAnghIhOepNVQXERCBuiEgEV43TaWKioAI1JCARHgN4evWIiACuSUgEZ7bplHFREAE8kxAIjzPraO6iYAI5IWARHheWkL1EAERyBOBWBE+bNiwPNVRdREBERABERABERABERABERABERCBQhBIFOFdu3YtxAPqIURABERABERABERABERABERABEQgLwQkwvPSEqqHCIiACIiACIiACIiACIiACIhA4QlIhBe+ifWAIiACIiACIiACIiACIiACIiACeSEgEZ6XllA9REAEREAEREAEREAEREAEREAECk9AIrzwTawHFAEREAEREAEREAEREAEREAERyAsBifC8tITqIQIiIAIiIAIiIAIiIAIiIAIiUHgCEuGFb2I9oAiIgAiIgAiIgAiIgAiIgAiIQF4ISITnpSVUDxEQAREQAREQAREQAREQAREQgcITkAgvfBPrAUVABERABERABERABERABERABPJCQCI8Ly2heoiACIiACIiACIiACIiACIiACBSegER44ZtYDygCIiACIiACIiACIiACIiACIpAXAhLheWkJ1UMEREAEREAEREAEREAEREAERKDwBCTCC9/EekAREAEREAEREAEREAEREAEREIG8EJAIz0tLqB4iIAIiIAIiIAIiIAIiIAIiIAKFJyARXvgm1gOKgAiIgAiIgAiIgAiIgAiIgAjkhYBEeF5aQvUQAREQAREQAREQAREQAREQAREoPAGJ8MI3sR5QBERABERABERABERABERABEQgLwQkwvPSEqqHCIiACIiACIiACIiACIiACIhA4QlIhBe+ifWAIiACIiACIiACIiACIiACIiACeSEgEZ6XllA9REAEREAEREAEREAEREAEREAECk9AIrzwTawHFAEREAEREAEREAEREAEREAERyAsBifC8tITqIQIiIAIiIAIiIAIiIAIiIAIiUHgCEuGFb2I9oAiIgAiIgAiIgAiIgAiIgAiIQF4ISITnpSVUDxEQAREQAREQAREQAREQAREQgcITkAgvfBPrAUVABERABERABERABERABERABPJCQCI8Ly2heoiACIiACIiACIiACIiACIiACBSegER44ZtYDygCIiACIiACIiACIiACIiACIpAXAhLheWkJ1UMEREAEREAEREAEREAEREAERKDwBCTCC9/EekAREAEREAEREAEREAEREAEREIG8EJAIz0tLqB4iIAIiIAIiIAIiIAIiIAIiIAKFJyARXvgm1gOKgAiIgAiIgAiIgAiIgAiIgAjkhYBEeF5aQvUQAREQAREQAREQAREQAREQAREoPAGJ8MI3sR5QBERABERABERABERABERABEQgLwQkwvPSEqqHCIiACIiACIiACIiACIiACIhA4QlIhBe+ifWAIiACIiACIiACIiACIiACIjDxEvj3339dnz593EILLeT222+/VBBnn322Gzp0qLvkkktcmzZtWgSaRHiLYNVFRUAEREAEREAEREAEREAEREAE8kAAEb733nu7K664wp1xxhnu8MMPj63W6aef7o488ki31157ucsvv1wivFqN991337nOnTu7KaaYolqXLMR1JkyY4P744w835ZRTFuJ59BAiIAIiIAIiIAIiIAIiIAIiYAQQ4r1793ZXXnmlO+uss9yhhx7aCA4R8MMOO8ztueeeXqy3ZJmoIuFvv/22W3zxxd2bb77p/63y/wTmmmsut++++zbpjGmMHn30UbfVVlu5iy++2O20004TNc5rr73WXXrppe6DDz5wbdu2dXPMMYe74IIL3JprrjlRc0l6+O+//94dddRR7r333nP9+vVzPXv2zA2niblfb7fddu6WW25x0047raONJplkkty0S9aKPP/88+6iiy7yfevxxx93M888s//p8OHD3frrr+++/fZbv/huvvnmWS/pF+TXXnvNLbPMMu7MM8/M/Dt9UQREQAREQAREIH8EiHJHhXhrCnCITFQi/K233nJLLLGERHjMWJhpppm8AI96hNKGDZ2XTnzMMce4k08+uaIRRgQe0VrPpX///u7YY491O++8s1t99dV9NsHnn3/uNtxwQ7/vRKUpARuLfHLjjTe6HXbYITeYqtGvc/MwZVRkzJgxbsYZZ3T8m3E9YMCAMn6dn6+ef/757qCDDvIV+uabb9yss87q/xunWN++ff1/L7/88u7FF1/MXGmyp0aNGuV23313d9VVV2X+nb4oAiIgAiIgAiKQTwKhEP/999/diSee2CoRcKNRcxFOPj6Rpx9++MGNHj3a/fnnn26qqaZyc889t1tppZXcbrvt5rp3716V1pMIT8ZYiQgnpePVV1/1WQWTTjpp2W2EUUzE6rPPPiv7t3n5wW+//eYjbSeddJI75JBD8lKt3NejHBE+btw4d/vtt7vHHnvM97effvrJHXzwwe7oo48u+Zw4iHCSUM4777wGEZb2w+b265KVCr7w0Ucf+UkfQchzzTnnnG777bf3zrBKxlQ5945+d+DAgf7elKefftqtuuqqzblczX6bJMKfffZZt9pqqznad//993cXXnhh5jpWU4TTJ+n/K664ot9zpiICIiACIiACIlAbAibEuTuHtaFLWqvUXIQj4DCK9thjD58C2aFDBx9xePfdd919993nvv76a29sI3KaWyTCkwlWIsKb2x4Yo9ddd51PD63XggNpvfXWcyNGjHDTTTddvT5Gq9c7qwgn6kiWAanRYckioq655hofubSSVYS3Fownn3zSbbDBBm78+PFNbrnccsu5J554wk0++eStVR230UYbuQcffNBHjpl3W+o00JZ+oCQRzn3feecdx7kg6667blkZONUU4ZNNNplv8wMPPNBRVxUREAEREAEREIHaEMCeZFvtRCvCF1lkEXfTTTc1of/PP/94A/y0007zKYChQV1JU0mES4RX0m/SfsNecPbSjx07ttqXLvT1Solwc8whpCkcpLjlllu6VVZZxXF+Af/MPvvsiYyImK+88sqNBG6eRDhOG7J9yKRA4DHP8Ty33nqru+uuu/xzcYLnZZdd1ir94Oeff/YZHX/99ZePwnNYSb2WNBFe6TNJhFdKTr8TAREQAREQgXwSsCi4banFFmuNA9mMRi4i4Uki3CrJ/r1ff/3Vffjhh81qRYlwifBmdaCYH/PqAkQLe0lUshMoJcKPO+64hnMGOPSPrQsIoSyFPc2c/fDpp5+6tdde26exU/IkwklD5hUYlGjqN4eHPfLII659+/b+bIE0Z0MWHlm+g9jHmUR54403PL96LRLh9dpyqrcIiIAIiIAItA4BHcz2H2fS0UuJcPZMnnDCCf7AoGh65o8//uijNvfee69PayYleI011vAp7PPPP3+jliwlwsu5Fhd+6aWX/AnPGPtEtqgb+9c5FGizzTZr0otIqSWtnpRP/pv0ewzsbbfdtuEgofBH999/v09XpN5EqOabbz6fDUCELJoqSnrjueee626++WZvuHfs2NHNMsssft8hex9L7S8lHZ3v8gwvv/yy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f6yUekVZ3OnoYT2pPxFsWLBdhbbmftSNwz7hQ9TZ0rgtQ4lrJO21Zw5lTJMOzjNQyFRgrqb+ODFCh18lYy1sdwQ26wHZBYwHIvDhK+u4Plk5OKqI/LMOkPHD3Eh9+D5rnYoIiIAIiIAIiEB9Eai5CK8vXKptUQkgFjHeETFZTkUvKgc9V/EJ4JzhveoUnF849VREQAREQAREQAREQARaj4BEeOux1p1yTIBIJBEuok3RE8VzXG1VTQTKJmB7jYmGk/URnrNQ9sX0AxEQAREQAREQAREQgbIJSISXjUw/KBoBtjCw95702lKpuUV7dj3PxEWAtH4yPkhzJlWbd6yriIAIiIAIiIAIiIAItC4BifDW5a275YQAJ60T8SYSSPo5e9OJhrMnXEUEikqA09Y5SJJCf+ed4ioiIAIiIAIiIAIiIAKtS0AivHV56245IMABcBwI9u233/oDkTbaaCN/wJ4EeA4aR1UQAREQAREQAREQAREQgYITkAgveAPr8URABERABERABERABERABERABPJDQCI8P22hmoiACIiACIiACIiACIiACIiACBScgER4wRtYjycCIiACIiACIiACIiACIiACIpAfAhLh+WkL1UQEREAEREAEREAEREAEREAERKDgBCTCC97AejwREAEREAEREAEREAEREAEREIH8EJAIz09bqCYiIAIiIAIiIAIiIAIiIAIiIAIFJyARXvAG1uOJgAiIgAiIgAiIgAiIgAiIgAjkh4BEeH7aQjURAREQAREQAREQAREQAREQAREoOAGJ8II3sB5PBERABERABERABERABERABEQgPwQkwvPTFqqJCIiACIiACIiACIiACIiACIhAwQlIhBe8gfV4IiACIiACIiACIiACIiACIiAC+SEgEZ6ftlBNREAEREAEREAEREAEREAEREAECk5AIrzgDazHEwEREAEREAEREAEREAEREAERyA8BifD8tIVqIgIiIAIiIAIiIAIiIAIiIAIiUHACEuEFb2A9ngiIgAiIgAiIgAiIgAiIgAiIQH4ISITnpy1UExEQAREQAREQAREQAREQAREQgYITkAgveAPr8URABERABERABERABERABERABPJDQCI8P22hmoiACIiACIiACIiACIiACIiACBScgER4wRtYjycCIiACIiACIiACIiACIiACIpAfAhLh+WkL1UQEREAEREAEREAEREAEREAERKDgBCTCC97AejwREAEREAEREAEREAEREAEREIH8EJAIz09bqCYiIAIiIAIiIAIiIAIiIAIiIAIFJyARXvAG1uOJgAiIgAiIgAiIgAiIgAiIgAjkh4BEeH7aQjURAREQAREQAREQAREQAREQAREoOAGJ8II3sB5PBERABERABERABERABERABEQgPwQkwvPTFqqJCIiACIiACIiACIiACIiACIhAwQlIhBe8gfV4IiACIiACIiACIiACIiACIiAC+SEgEZ6ftlBNREAEREAEREAEREAEREAEREAECk5AIrzgDazHEwEREAEREAEREAEREAEREAERyA8BifD8tIVqIgIiIAIiIAIiIAIiIAIiIAIiUHACEuEFb2A9ngiIgAiIgAiIgAiIgAiIgAiIQH4ISITnpy1UExEQAREQAREQAREQAREQAREQgYITkAgveAPr8URABERABERABERABERABERABPJDQCI8P22hmoiACIiACIiACIiACIiACIiACBScgER4wRtYjycCIiACIiACIiACIiACIiACIpAfArkR4Xvvvbe7/fbb3e+//+66dOniVlppJXfyySe7+eabz9Nq3769++effxqRe+6551z//v3dmDFj3LPPPtvos5133tl9/PHH7uWXX/a/ve6669wOO+zgvv32W7fPPvu4p556ynXs2NH16NHDXXHFFW6WWWZp9D0u9sYbb7gjjzzSPf/8865Dhw5urbXWcmeccYabc845/b34++qrr+4GDx7s/21l8cUXdxtttJE75ZRT8tPSqokI5JDA+uuvn2n8xo195ojFFlvMff755+7vv/92Xbt2dZtttpk76aST3JRTTun+/PNPd8ghh7hbb73VjRs3zi288MJ+/K666qp+LHfr1s1dddVVngq/Zy5hnhg+fLibe+653f777++Yl6wwjxx//PHu2GOPbfhb37593WuvvebnApV8EZhrrrnc119/7ed12nrttdf27T/55JO7Bx54wG288cZu7NixbrLJJvMVZ61Yfvnl3RdffOG/H/YR5nL6xmeffdbkIRdZZBE3dOhQvz7RBzfYYAPfR2aaaaYm3610naOv//XXX+7UU09t6KM837777uv69Onj2rRp4++19NJL+3qffvrpDfdeb731fF2oP88R9l++tOaaa7rHH388cW3cZptt3AsvvNDkWRhHTz/9dL4aveC1SZvvmINWXnnlRgTatWvn5zb77JtvvnGzzjpro+8k2Vb0OSvHHXect8c++OADt+CCCzb8vZQ9lTRv2wXeeecdP2cz51LX2Wef3WG7HX744b5PlxovXOe2227z/f3DDz90008/vdt22239GsA4t/Luu+96Ww6bkcKY5Tfwuvbaa91RRx3lfv75Zzf11FP7z/j/ddZZx3+X8fTEE080YgaLY445xtudBx98sHvvvffcDDPM4NZdd1135ZVXpq49Be+iZT9eEfs09v8PP/zg1x5siV69evl+go4IS3RcMVYnmWSSWIbYHvTFpDFeNnj9IBcEciPC6VxMwgceeKD77rvv3AUXXOCNcwzstm3b+s7cr18/b0hZWWKJJdydd97pevfu7b7//ns/AVOY+GeccUY/6WKEhyKcwYEhhcHNJP/MM894wwxDLPzeW2+95VZccUUv0rk+Bv2FF17ojbo333zTi3Zb2HAavP32227mmWf295cIz0XfViXqgMDVV1+dafzGjf1OnTr5MXvQQQe5VVZZxRuIZ555pttwww3dDTfc4M4++2wvms8//3wvjl5//XX/2VJLLdVEhG+33XZu0KBB7ogjjvBG2CuvvOJ/f9hhh3nhQ+FeEyZMcI8++mjDPCQRnt9ORnthjCMycciyFmy//fa+P1RThFsf5D6ffPKJO/fcc9348ePdq6++2kTwVLrO0dcRww8++GCTPopxxxpGySLCL7vsMnfzzTc3NNw000zjnVlJayPiZtSoUX7d3HPPPd3111/vxVLnzp39WqfSegTS5juzRwhmYJNQsHFwlpQS4UnzK9dAGNDeo0ePdrvuuqu3g6yUsqfSrss1rF7M11NMMYV76aWXvO3HPM7cWmq8EEDBPqNf4tDFPjvttNN88IZACzbl+++/75Zddllvy+21115u0kkn9ffZaqut/N9wTF1yySXummuucb/88ou75ZZbfGAFm26BBRbwdUA8sRZYIRCDvYdzCzsRZxgiHmccwipt7Wm93lIfdypqn77xxhu9UwcnPWvCMsss4x577DFvRySNq3///ddrEgraZuDAge7uu+/2/49jGKcXIjxujNdHa6uWUQK5EuFhZAqPJYY1Cz+epFAghw/BxMdEyGTMAkHBq48Xk4jGHHPM0ei388wzjzc2MMSiJbwH0YOffvrJ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Fx66y3rAmsGaYNkhOMAQ5XHZIQRf2M748MMPx+4JR1DBgTWCZ+B8IeYBHHqMR8Zf0tpT+16UrxoUrU+jG5jnyb5AhKMXOI+G4BzPyhaptHHFWoL9kXVPuI1x7qlSfwRyI8LrD51qLAIiIAIiIAKtT4B0V6LuiCCyPxAfKiIgAiIgAiIgAvVDQCK8ftpKNRUBERABERABN3bsWH+A1B133OGj3bZ/VWhEQAREQAREQATqg4BEeH20k2opAiIgAiIgAiIgAiIgAiIgAiJQAAIS4QVoRD2CCIiACIiACIiACIiACIiACIhAfRCQCK+PdlItRUAEREAEREAEREAEREAEREAECkBAIrwAjahHEAEREAEREAEREAEREAEREAERqA8CEuH10U6qpQiIgAiIgAiIgAiIgAiIgAiIQAEISIQXoBH1CCIgAiIgAiIgAiIgAiIgAiIgAvVBQCK8PtpJtRQBERABERABERABERABERABESgAAYnwAjSiHkEEREAEREAEREAEREAEREAERKA+CEiE10c7qZYiIAIiIAIiIAIiIAIiIAIiIAIFICARXoBG1COIgAiIgAiIgAiIgAiIgAiIgAjUBwGJ8PpoJ9VSBERABERABERABERABERABESgAAQkwgvQiHoEERABERABERABERABERABERCB+iAgEV4f7aRaioAIiIAIiIAIiIAIiIAIiIAIFIBAzUT466+/XgB8egQREAEREAEREAEREAEREAEREIGiElhqqaWq/mg1E+FVfxJdUAREQAREQAREQAREQAREQAREQARyTkAiPOcNpOqJgAiIgAiIgAiIgAiIgAiIgAgUh4BEeHHaUk8iAiIgAiIgAiIgAiIgAiIgAiKQcwIS4TlvIFVPBERABERABERABERABERABESgOAQkwovTlnoSERABERABERABERABERABERCBnBOQCM95A6l6IiACIiACIiACIiACIiACIiACxSEgEV6cttSTiIAIiIAIiIAIiIAIiIAIiIAI5JyARHjOG0jVEwEREAEREAEREAEREAEREAERKA4BifDitKWeRAREQAREQAREQAREQAREQAREIOcEJMJz3kCqngiIgAiIgAiIgAiIgAiIgAiIQHEISIQXpy31JCIgAi1M4Met5mzhO+Tv8jPc8UX+KqUaiYAIiIAIiIAIiEAdE5AIr+PGU9VFQARal0C9iPC2naZ3E0aNqAqcehXhw4YNc2PHjnVzzz13VTjoIiIgAiIgAiIgAiJQLQIS4dUiqeuIgAgUnkA9iPB2s87rpjtvsPtxm3mcm/BPs9ukXkX4lltu6eaZZx53+umnN5uBLiACIiACIiACIiAC1SSQGxH+6KOPuvPPP9+9+OKL7q+//nILL7ywO/LII90WW2wR+7xnn322O+yww9zxxx/vTjjhhGoy0bVEQARamcBLL73k+vbt69599103//zzu/POO8+tttpqqbVord+Elai6CG/b1k22ak/XcZXNXfs5FnCjbzrdjXvydn/LLte/49pMPlUTBhN+HeFG7LlMIpt2s83npjv30VgR3m6mOdzkG+/pJl10Jf/7n/dPZ8x3kkT4TTfd5E499VQ3dOhQN9tss7mDDz7Y7bPPPrH1OvTQQ91rr73mnn766VbrWS0hwgcNGuQ233xz988/TZ0bRN73228/9/jjj7tpppnGHX744a5Pnz6NnhcGcOLfU001levVq5c77bTTXMeOHRu+B6P999/fffrpp27OOed0Rx11lNtxxx1juSXVhwyAM844w1111VXul19+cfPNN59fKzfbbLOG6/Df9913X+x1P/zwQ7fAAgs0+uzJJ590l112mXvhhRfcOuus46677jr/ebnXabUOoBuJgAjknsC3337rjj76aD8Xtf1vPVx66aXdOeec47p37x5b93///dfrAtb/77//PvH5brzxRrfXXnu5zz77zM0yyyyx39ttt93cF1984Z566qmqc2J9xAHM/WeddVbHvfr16+fatWvXcC+0zhFHHOH47p9//um23357h7aZbLLJGr6T5Tr2Ze4Fu8cee8z/if9XyTeB3IjwFVdc0QvwKaaYwg/E0aNHuzZt2vjOtOaaazZQxNDBuLn55pv93yTC893BVDsRKEWARXDxxRd3++67r9tqq63cPffc40X422+/nZhK3Fq/ida9miK8zeRTu85HXOnadZnVjblvgPvz/ZfdPz9+49z4sf627Wbq5lzb/1+w+dtUu5/gJowY5n67rF8i1iQR3mHFjV2nPme7cS8/5MY9fZf7+7vP3YSfh5dqnlgR/uCDD3pD6Prrr3err766n7u32247d/XVV/t/W0EAYnggBldZZZW6FeF///23u/zyyx3OhPHjxzsMwbAgyjEeu3bt6tekjz/+2DskeO5tt93Wf5W1a6GFFnJ77723O+CAA9zw4cPdNtts49ZYYw13xRVX+O+8//77bskll3QXXnih22CDDdyrr77qdtllFy94Q4d0qfqcfPLJfu3EUY2D5M477/Rin+txfcp3333n19mwYLjee++97vXXX28wBHlWHGQ8C//eZJNNfIbBdNNNV9Z1SnY0fUEERGCiI7DWWmu5bt26uR122MFNPvnk7pRTTnFvvPGG++ijj9yUU07ZhAeOXwJ26IQ0EX7WWWd5rYBj9KKLLmpyHZzHOPzZrsS9qlmee+457zhlLWC+feutt3w9mJeZQ62wDjz00EN+bUGcszawnvL/lKzX4bu33HKL23XXXR3O55133tk7UZn7VfJNIDci/Pnnn/fRBQw1/o0xjjGAQWeCG8OPv+PltyIRnu8OptqJQCkCRP0Q3M8++2zDV3G8Eb0j8hZXWus30XtXU4R3OvS/hXfG2d0vx2/j/v2jsRiKe2ai150OvMCN2H/1/77/WyLWOBHefq5F3LT973a/DTjqPwF+Z6kmafR5XCR866239gbSNddc0/Bdor4YNg8//LD/G1kNzOfLLbecW3TRRd0rr7xSlyKcaAX1R4wedNBB3lCKivC77rrLG0Bff/2169y5s39+DK6BAwc6osqUSy+91Ec5YGTljjvu8CL7999/905nIjcjR470otlK//79/VqIgKZkqQ9r5KSTTtoo6tKjRw8fwcbIjSs//fSTH3P333+/W2ml/2VKUHAIHHfcce6ZZ55xiy22WMm+k3Sdkj/UF0RABCY6AjgCyQqywv8zh5Idi0APC87BjTbayM+jhxxySKoI53OuwXzLvDz99NM3ulbv3r0dugNn5K+//lp17tHnwiGAs5p7UnAgzD777O6RRx7xjlgKohsR/tVXXzVE70tdh9/BZfnll/fOXNYTlfohkBsRHkWG133PPff00QDEN+WTTz7xKSoYKj///LP3/EiE109nU01FII4Ahj8C7sADD2z4+JJLLnF4sr/88stYaC39GyKNk0wyiU/lNVFFRUyET7rEam7KbQ9x7f/bf030+vfbz3fjX/rfPEXpuPb2boqefVzbTtO5CaN/dWMfG+jG3HFBw+ft5+rupj1jkBvZb2P399D3MnWMaU69x41/8QH3xwNXN/p+h2XWcVNsfaBr33Uu989P37m/PnnTTbb6lo3S0Tsf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ERABERABERABERABERABERCBhCUgEZ6wt0YdEwEREAEREAEREAEREAEREAERaG0EJMJb2x3V9YiACIiACIiACIiACIiACIiACCQsAYnwhL016pgIiIAIiIAIiIAIiIAIiIAIiEBrIyAR3truqK5HBERABERABERABERABERABEQgYQlIhCfsrVHHREAEREAEREAEREAEREAEREAEWhsBifDWdkd1PSIgAiIgAiIgAiIgAiIgAiIgAglLQCI8YW+NOiYCyU3gm2++sZ49ezoI5eXldvz48cr/JzcZXb0IiIAIiIAIiIAIiEBLJiAR3pLvnvouAq2UwKeffmq33nqrXbx40VJTU23VqlWWm5tr+/fvb6VXrMsSAREQAREQAREQARFIFgIS4clyp3WdItCCCBw8eNBuu+22ShG+cuVKW7NmTb1EOKK9rKysytUeOXLEbrrppjoJHD161J555hnbtm2bFRUV2S233GLPPfecjRw5ss5j9QUREAEREAEREAEREAERqA8BifD6UNJ3REAE4kqgsSIcz3m7du0M0Z2enl7Z5+zsbGvbtm2d13DgwAHndc/JybE2bdrY2rVr7Y033jA88/UR8XWeQF8QAREQAREQAREQARFIegIS4Un/CAiACCQegcaK8FOnTtm1115rpaWlV+yi+vbta0899ZRNnz79irWphkRABERABERABERABJKXgER48t57XbkIJAyBzZs32/z58+3w4cPWu3dvu+uuu+zNN9+sEo7+0ksv2cCBA23Xrl124cIFGzZsmC1dutRuuOGGyuvIy8uzwYMH28mTJ2u9tmXLltnrr79uX331lXXv3t1mz55t06ZNi3lMv379bMaMGfbYY48lDC91RAREQAREQAREQAREoOUSkAhvufdOPReBVkGA/OuHHnrI5s2b5/5GGK9bt842bdpURYTPnDnT5Wvfd999ztM9Z84c91285h06dHAs9u3bZ+PHj7fPP/+8RjbkeL/yyiu2aNEiJ/bPnj1rGRkZdscdd1Q5hpzwFStWOLH+8ccfW9euXVsFb12ECIiACIiACIiACIjA1SUgEX51+evsIpD0BO68804bOnSovfzyy5Us6hOOXlJS4rzmzz77rD3xxBPu2C1bttjDDz/svOM33nijPfjgg+6z9u3bu88R3ISrv/XWWzZ69Oga2Y8bN87eeecdJ8537Nhh99xzT9LfJwEQAREQAREQAREQARG4MgQkwq8MR7UiAiLQCAKFhYXWuXNn27t3r/NKe6uPCOe7U6ZMccJ648aN7tDi4mI7duyY21P8ww8/tNdee8169epl7733nivMtn37dhs1apT7HoXXajL2KD906JC9++67tmTJEueVf+CBBxpxhTpEBERABERABERABERABKoSkAjXEyECInDVCLAlGN7sL774wvr06dNgEU4uNyHoO3fujHkNX3/9tatqTpXzsWPH2vr1611IO+etry1YsMB5xamQLhMBERABERABERABERCBphKQCG8qQR0vAiLQaALnzp2zTp062e7du+3uu+9usAifNGmS82pv2LChxj7Q7ogRI1zO+datW41Q8zNnztTqCQ83Rjg6Ye1sfyYTAREQAREQAREQAREQgaYSkAhvKkEdLwIi0CQCQ4YMsf79+9vq1asbJMJPnz5tbB9GgbXJkyfH7ENZWZldd911tnDhQlfdnC3Mevbs6XLHR44cWa9+E46+fPly562XiYAIiIAIiIAIiIAIiEBTCUiEN5WgjhcBEWgSAfLBKcz2+OOPW05OjnXs2NFtQzZ16tQq1dEXL17swsmzsrLsyy+/dNXRyfPes2ePpaWlWXl5ufN2Dxo0yBVmY5sytiL76KOPXAV1X9181qxZtmbNGmPLM76LVxyPPN5yCrZdunTJbr/9dkPAf/DBB+48CHH6IxMBERABERABERABERCBphKQCG8qQR0vAiLQZAIUUZs7d64rpsYe4NnZ2U4gv/322y5sHKFN/vdnn31m+fn5rsL5mDFj7Pnnn3eF3TC2FEMov//++64wG6KbvcTxglMp3RtinS3KVq1a5XLDe/ToYRMnTjS2LsvNzXVbkrHfeEpKirFH+JNPPmkTJkxo8jWqAREQAREQAREQAREQARGAgES4ngMREAEREAEREAEREAEREAEREAERiBMBifA4gdZpREAEREAEREAEREAEREAEREAEREAiXM+ACIiACIiACIiACIiACIiACIiACMSJgER4nEDrNCIgAiIgAiIgAiIgAiIgAiIgAiIgEa5nQAREQAREQAREQAREQAREQAREQATiREAiPE6gdRoREAEREAEREAEREAEREAEREAERkAjXMyACIiACIiACIiACIiACIiACIiACcSIgER4n0DqNCIiACIiACIiACIiACIiACIiACEiE6xkQAREQAREQAREQAREQAREQAREQgTgRkAiPE2idRgREQAREQAREQAREQAREQAREQAQkwvUMiIAIiIAIiIAIiIAIiIAIiIAIiECcCEiExwm0TiMCIiACIiACIiACIiACIiACIiACEuF6BkRABERABERABERABERABERABEQgTgQkwuMEWqcRAREQAREQAREQAREQAREQAREQgasmwo8dOyb6IiACIiACIiACIiACIiACIiACIpCwBK6//vor3rf/B2YdZ5nan8fTAAAAAElFTkSuQmCC)

2. Check out the `oidc`  packaging branch and reset HEAD to the commit which the package was built from:

    ```shell
    git checkout oidc
    git reset --hard ea67d6c # Replace with the actual commit ID
    ```

3. Check that you’re on the correct commit. The commit message should be "Copy oidc variant files".

4. Get the merge-base with the `main` branch:

    ```shell
    BASE=$(git merge-base HEAD main)
    ```

5. Check that the merge-base is the same as the second commit ID from the candidate release version.

6. Tag the merge-base with `broker-x.y.z`:

    ```shell
    VERSION=x.y.z
    git tag -s "${VERSION}" -m "Release broker version ${VERSION}" "${BASE}"
    ```

7. Tag the commit itself with oidc-x.y.z:

    ```shell
    git tag -s "${VERSION}" -m "Release authd-oidc version ${VERSION}"
    ```

8. Push the tag and force push the commit to the packaging branch:

    ```shell
    git push --tags
    git push --force
    ```

9. Repeat for the msentraid broker:
   1. Find the commit ID of the [candidate release](https://snapcraft.io/authd-msentraid/releases)
   2. Check out the branch, reset the commit, tag the commit, push tag and commit:

        ```shell
        COMMIT=a254a2c # Replace with the actual commit ID
        git checkout msentraid
        git reset --hard "${COMMIT}"
        git tag -s "${VERSION}" -m "Release authd-msentraid version ${VERSION}"
        git push --tags
        git push --force
        ```
   3. Request a build of the [authd-msentraid snap](https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-msentraid)

10. Repeat for the google broker:
     1. Find the commit ID of the [candidate release](https://snapcraft.io/authd-google/releases)
     2. Check out the branch, reset the commit, tag the commit, push tag and commit:

        ```shell
        COMMIT=3014df4 # Replace with the actual commit ID
        git checkout google
        git reset --hard "${COMMIT}"
        git tag -s "${VERSION}" -m "Release authd-google version ${VERSION}"
        git push --tags
        git push --force
        ```

    3. Request a build of the [authd-google snap](https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-google)

## Release the broker snaps with the new version

1. Manually trigger import of the git repo:
   * Go to [https://code.launchpad.net/\~ubuntu-enterprise-desktop/authd/+git/authd](https://code.launchpad.net/~ubuntu-enterprise-desktop/authd/+git/authd)
   * Click "Import Now"

2. Wait until the import succeeds.

3. Request builds of the snaps by clicking "Request builds" at the bottom of the following pages.  Don’t fill out any fields, the defaults are fine.
   1. [https://launchpad.net/\~ubuntu-enterprise-desktop/authd/+snap/authd-oidc](https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-oidc)
   2. [https://launchpad.net/\~ubuntu-enterprise-desktop/authd/+snap/authd-msentraid](https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-msentraid)
   3. [https://launchpad.net/\~ubuntu-enterprise-desktop/authd/+snap/authd-google](https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-google)

4. Wait until the builds succeed.

5. Check that snapcraft.io lists the builds with the correct versions
   * [https://dashboard.snapcraft.io/snaps/authd-oidc/](https://dashboard.snapcraft.io/snaps/authd-oidc/)
   * [https://dashboard.snapcraft.io/snaps/authd-msentraid/](https://dashboard.snapcraft.io/snaps/authd-msentraid/)
   * [https://dashboard.snapcraft.io/snaps/authd-google/](https://dashboard.snapcraft.io/snaps/authd-google/)

## Test the published packages

Install the authd package from the edge PPA and the snaps from the edge channel and try logging in via authd.

## Copy package from edge PPA to stable PPA

1. Go to [https://launchpad.net/\~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+packages](https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+packages)
2. Click "Copy packages" in the top right
3. Select the authd packages for all currently supported Ubuntu versions
4. Destination PPA: authd stable
5. Destination series: The the same series
6. Copy options: Rebuild the copied sources (because we build for more architectures in the stable PPA than in the edge PPA)

## Promote snap from edge channel to stable

1. Go to [https://snapcraft.io/authd-oidc/releases](https://snapcraft.io/authd-oidc/releases)
2. In the "0.x/edge" row, drag-and-drop both the AMD64 and the ARM64 releases to the "0.x/stable" row (do NOT use "Promote" to promote to latest/stable, we don't use that track).
3. Click "Save"
4. Repeat the same for:
   1. [https://snapcraft.io/authd-msentraid/releases](https://snapcraft.io/authd-msentraid/releases)
   2. [https://snapcraft.io/authd-google/releases](https://snapcraft.io/authd-google/releases)

## Update the stable-docs branch

After publishing a new release, the `stable-docs` branch should contain the same version of the documentation as the release branch, so we have to update it.

1. Check out the `stable-docs` branch of the authd repo:

   ```shell
   git checkout stable-docs
   ```

2. Reset it to the index from the release (make sure there are no uncommitted changes which you don’t want to lose):

   ```shell
   git reset --hard "release-$VERSION"
   ```

3. Force push the branch:

   ```shell
   git push --force-with-lease origin stable-docs
   ```

## Upload the source package to the Ubuntu archive

Since authd is also in the Ubuntu archive now, we also need to upload new releases there, targeting at least the next Ubuntu release, and maybe also existing ones, although that will require SRUs.

We don’t have anyone with uploads rights in our squad currently, so for now we have to ask Didier to sponsor the upload.
