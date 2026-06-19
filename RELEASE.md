# Releasing authd and the brokers

This document describes the process of releasing stable and bugfix releases of
the authd Debian packages and the broker snaps.

## Bugfix release - brokers

### Prepare release branch

1. Find the latest broker tag:

    ```shell
    git fetch --tags
    LATEST_BROKER_TAG=$(git tag -l 'broker-[0-9]*.[0-9]*.[0-9]*' --sort=-version:refname | head -n1)
    echo "Latest broker tag: $LATEST_BROKER_TAG"
    ```

2. Set the new version for the broker (increase the patch version):

    ```shell
    OLD_VERSION=${LATEST_BROKER_TAG#broker-}
    IFS='.' read -r MAJOR MINOR PATCH <<< "$OLD_VERSION"
    VERSION="$MAJOR.$MINOR.$((PATCH + 1))"
    echo "New version: $VERSION"
    ```

3. Check out a new branch from the latest broker tag:

    ```shell
    git checkout -b "release-brokers-$VERSION" "$LATEST_BROKER_TAG"
    ```

4. Cherry-pick the commits with the bug fixes to the release branch.

5. Create a tag for the new broker version:

    ```shell
    git tag -s "broker-${VERSION}" -m "Release broker version ${VERSION}"
    ```

6. Push the release branch and tag:

    ```shell
    git push --no-verify origin "release-brokers-$VERSION" "broker-${VERSION}"
    ```

7. Open a PR from the release branch to main, using the `e2e-tests` tag to
   trigger e2e-tests. Describe the changes in the PR description.

    ```shell
    gh pr create --base main --head "release-brokers-$VERSION" \
        --label "e2e-tests" \
        --title "Release broker version ${VERSION}"
    ```

### Prepare msentraid release branch

1. Check out the `msentraid` branch:

    ```shell
    git checkout msentraid
    ```

2. Check that there are no unexpected changes compared to the latest msentraid tag:

    ```shell
    LATEST_MSENTRAID_TAG=$(git tag -l 'msentraid-[0-9]*.[0-9]*.[0-9]*' --sort=-version:refname | head -n1)
    git log -p "${LATEST_MSENTRAID_TAG}"..HEAD
    ```

3. Merge the new broker tag into the `msentraid` branch:

    ```shell
    git merge --no-ff "broker-$VERSION"
    ```

4. Prepare the snapcraft.yaml for building this broker variant

    ```shell
    ./snap/scripts/prepare-variant --copy --broker msentraid
    ```

    * If this produced any changes to the snapcraft.yaml, commit them:

        ```shell
        git add snap/snapcraft.yaml
        git commit -m "Copy msentraid variant files for $VERSION"
        ```

5. Try building the snap locally (to reduce the chance that the launchpad build will fail):

    ```shell
    snapcraft pack
    ```

6. Create a tag for the new msentraid broker version:

    ```shell
    git tag -s "msentraid-${VERSION}" -m "Release authd-msentraid version ${VERSION}"
    ```

7. Push the branch and tag:

    ```shell
    git push --no-verify origin msentraid "msentraid-${VERSION}"
    ```

### Prepare google and oidc release branches

Repeat the same steps as for the msentraid release branch, but replace
`msentraid` with `google` and `oidc` respectively:

```shell
(
set -euxo pipefail

VARIANTS=("google" "oidc")
for VARIANT in "${VARIANTS[@]}"; do
    # Check out the branch
    git checkout "$VARIANT"

    # Check that there are no unexpected changes compared to the latest tag
    LATEST_TAG=$(git tag -l "${VARIANT}-[0-9]*.[0-9]*.[0-9]*" --sort=-version:refname | head -n1)
    echo "Latest $VARIANT tag: $LATEST_TAG"
    git log -p "$LATEST_TAG"..HEAD || true

    # Ask the user to confirm that the changes look good before proceeding
    printf "Do the changes for %s look good? (y/n) " "$VARIANT" >&2
    read REPLY < /dev/tty
    if [ "$REPLY" != "${REPLY#[Yy]}" ]; then
        :
    else
        echo "Aborting release process for $VARIANT. Please fix the issues and try again."
        exit 1
    fi

    # Merge the new broker tag into the branch
    git merge --no-ff "broker-$VERSION"

    # Prepare the snapcraft.yaml for building this broker variant
    ./snap/scripts/prepare-variant --copy --broker "$VARIANT"

    # If this produced any changes to the snapcraft.yaml, commit them
    if ! git diff --quiet snap/snapcraft.yaml; then
        git add snap/snapcraft.yaml
        git commit -m "Copy $VARIANT variant files for $VERSION"
    fi

    # Create a tag for the new broker version
    git tag -s "${VARIANT}-${VERSION}" -m "Release authd-${VARIANT} version ${VERSION}"

    # Push the branch and tag
    git push --no-verify origin "$VARIANT" "${VARIANT}-${VERSION}"
done
)
```

### Build and test the snaps

1. Wait for the CI jobs of the release branch PR to complete.

2. Manually trigger import of the git repo:
   * Go to https://code.launchpad.net/~ubuntu-enterprise-desktop/authd/+git/authd
   * Click "Import Now"

3. Wait until the import succeeds.

4. Request builds of the snaps by clicking "Request builds" at the bottom of the following pages.  Don’t fill out any fields, the defaults are fine.
   * https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-oidc
   * https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-msentraid
   * https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-google

5. Wait until the builds succeed.

6. Check that snapcraft.io lists the builds with the correct versions
   * https://dashboard.snapcraft.io/snaps/authd-oidc/
   * https://dashboard.snapcraft.io/snaps/authd-msentraid/
   * https://dashboard.snapcraft.io/snaps/authd-google/

7. Trigger the e2e-test workflow with the candidate build artifacts and wait for
   it to complete successfully. Note: This step might be unnecessary if the
   e2e-tests already succeded for the release branch.

   ```shell
   gh workflow run e2e-tests.yaml --ref main --field broker-snap-channel=candidate
   ```

### Promote snap from candidate to stable channel

1. Go to [https://snapcraft.io/authd-oidc/releases](https://snapcraft.io/authd-oidc/releases)
2. In the "0.x/candidate" row, drag-and-drop all releases to the "0.x/stable" row
   (do NOT use "Promote" to promote to latest/stable, we don't use that track).
3. Click "Save"
4. Repeat the same for:
   1. [https://snapcraft.io/authd-msentraid/releases](https://snapcraft.io/authd-msentraid/releases)
   2. [https://snapcraft.io/authd-google/releases](https://snapcraft.io/authd-google/releases)

### Merge release branch

Merge the release branch to main. This ensures that new releases to the edge
channel have a version greater than the stable release.

```shell
git checkout main
git merge --no-ff "release-brokers-$VERSION"
git push origin main
```

## Bugfix release - deb

TBD

## Stable release

### Prepare release branch

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
       PREVIOUS_TAG=$(git tag -l 'v[0-9]*.[0-9]*.[0-9]*' --sort=-version:refname | head -n1)
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

5. Push and open a PR. Use the changelog entry as the PR description.


### Download the source packages from the GitHub CI

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

### Rebuild the source packages

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

### Build the source package (obsolete if you download the source package from the GitHub CI)

Build the source package (and ensure that there are no lintian issues on the source package):

```shell
gbp buildpackage -S --git-ignore-new --git-export-dir=/tmp/authd-build
```

### Build the binary package (obsolete if you download the binary package from the GitHub CI)

Note that this should be performed for all the supported versions (noble and plucky at the moment):

1. If you didn't create an sbuild schroot before (TODO: Consider using sbuild with unshare instead):

    ```shell
    mk-sbuild <release> --distro ubuntu
    ```

2. Build the binary package to ensure it builds and that there are no lintian issues to address on the binary package:

    ```shell
    sbuild -A -v --build-dep-resolver=aptitude -d <release>-amd64 \
      "$(ls -t1 /tmp/authd-build/authd_*.dsc | head -n1)"
    ```

### Push the .changes file to the edge PPA

1. Push the files:

    ```shell
    for f in authd*/authd_*_source.changes; do
        dput ppa:ubuntu-enterprise-desktop/authd-edge "$f"
    done
    ```

2. Check that the builds are scheduled (usually takes \~2 minutes to show up): https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+builds?build_text=&build_state=all

3. Wait for the builds to complete (should take around 15 minutes)

4. Wait for the packages to be published (usually takes \~1h, but can take up to several hours): https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+packages?field.name_filter=&field.status_filter=&field.series_filter=

### Copy package from edge PPA to candidate PPA

1. Go to https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+packages
2. Click "Copy packages" in the top right
3. Select the authd packages for all currently supported Ubuntu versions
4. Destination PPA: authd candidate
5. Destination series: The the same series
6. Copy options: Copy existing binaries

### Copy snap release from edge to candidate channel

1. Go to https://snapcraft.io/authd-oidc/releases
2. For all architectures, drag and drop the release from the edge channel to the candidate channel
3. Click "Review changes" in the top right and then "Save"
4. Repeat the same for:
   1. https://snapcraft.io/authd-msentraid/releases
   2. https://snapcraft.io/authd-google/releases

### Install authd in a VM

1. Clean up previous installs (if any):

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

### Build the authd-msentraid snap

```shell
git checkout msentraid
git pull
snapcraft pack
```

### Build the authd-google snap

```shell
git checkout google
git pull
snapcraft pack
```

### Install the snaps

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

### Do the manual tests

Create a new tab in the [spreadsheet (internal)](https://docs.google.com/spreadsheets/d/1FV9r-e9M_Hm_Se2FAaVGHxnsfd-omsRozW43G0qeI8g) and go through the manual tests.

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

### Finalize the release branch

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

### Tag the broker branches with the version

1. Find the commit IDs which the [candidate release](https://snapcraft.io/authd-oidc/releases) of authd-oidc was built from.
   The first commit ID in the actual commit the release was built from, the second commit ID is the merge-base with the main branch.
   <br><br>
   ![image-find-commit-ids](https://github.com/user-attachments/assets/534281fa-f584-4491-824f-ae2c031f3e1e)

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
    git tag -s "broker-${VERSION}" -m "Release broker version ${VERSION}" "${BASE}"
    ```

7. Tag the commit itself with oidc-x.y.z:

    ```shell
    git tag -s "oidc-${VERSION}" -m "Release authd-oidc version ${VERSION}"
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
        git tag -s "msentraid-${VERSION}" -m "Release authd-msentraid version ${VERSION}"
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
        git tag -s "google-${VERSION}" -m "Release authd-google version ${VERSION}"
        git push --tags
        git push --force
        ```

    3. Request a build of the [authd-google snap](https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-google)

### Release the broker snaps with the new version

1. Manually trigger import of the git repo:
   * Go to https://code.launchpad.net/~ubuntu-enterprise-desktop/authd/+git/authd
   * Click "Import Now"

2. Wait until the import succeeds.

3. Request builds of the snaps by clicking "Request builds" at the bottom of the following pages.  Don’t fill out any fields, the defaults are fine.
   * https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-oidc
   * https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-msentraid
   * https://launchpad.net/~ubuntu-enterprise-desktop/authd/+snap/authd-google

4. Wait until the builds succeed.

5. Check that snapcraft.io lists the builds with the correct versions
   * https://dashboard.snapcraft.io/snaps/authd-oidc/
   * https://dashboard.snapcraft.io/snaps/authd-msentraid/
   * https://dashboard.snapcraft.io/snaps/authd-google/

### Test the published packages

Install the authd package from the edge PPA and the snaps from the edge channel and try logging in via authd.

### Copy package from edge PPA to stable PPA

1. Go to https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge/+packages
2. Click "Copy packages" in the top right
3. Select the authd packages for all currently supported Ubuntu versions
4. Destination PPA: authd stable
5. Destination series: The the same series
6. Copy options: Rebuild the copied sources (because we build for more architectures in the stable PPA than in the edge PPA)

### Promote snap from edge channel to stable

1. Go to https://snapcraft.io/authd-oidc/releases
2. In the "0.x/edge" row, drag-and-drop both the AMD64 and the ARM64 releases to the "0.x/stable" row (do NOT use "Promote" to promote to latest/stable, we don't use that track).
3. Click "Save"
4. Repeat the same for:
   1. https://snapcraft.io/authd-msentraid/releases
   2. https://snapcraft.io/authd-google/releases

## Post release steps

### Update the stable-docs branch

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

### Upload the source package to the Ubuntu archive

Since authd is also in the Ubuntu archive now, we also need to upload new releases there, targeting at least the next Ubuntu release, and maybe also existing ones, although that will require SRUs.

We don’t have anyone with uploads rights in our squad currently, so for now we have to ask Didier to sponsor the upload.
