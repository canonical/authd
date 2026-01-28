---
myst:
  html_meta:
    "description lang=en": "Configure authd and its identity brokers to enable
    authentication of Ubuntu devices with multiple cloud identity providers,
    including Google IAM, Microsoft Entra ID, and Keycloak."
---

(ref::config)=
# Configure authd for identity providers

This guide shows how to configure identity brokers to support authentication of
Ubuntu devices with authd and your chosen identity provider.

```{admonition} Logging in multiple users with authd
:class: tip
By default, the first user to authenticate and log in to a machine becomes the
"owner" and only they are allowed to log in.

For other authenticated users to log in, they must first be added as an
"allowed user".

The steps you need to follow when allowing more users are outlined in
[configure allowed users](ref::config-allowed-users) on the current page.

```

## Broker discovery

Create the directory that will contain the declaration files of the broker(s):

```shell
sudo mkdir -p /etc/authd/brokers.d/
```

Then copy the `.conf` file from the broker snap package corresponding to the
identity provider you want to use:

:::::{tab-set}
:sync-group: broker

::::{tab-item} Google IAM
:sync: google

```shell
sudo cp /snap/authd-google/current/conf/authd/google.conf /etc/authd/brokers.d/
```

::::

::::{tab-item} Microsoft Entra ID
:sync: msentraid

```shell
sudo cp /snap/authd-msentraid/current/conf/authd/msentraid.conf /etc/authd/brokers.d/
```

::::

::::{tab-item} Keycloak
:sync: keycloak

```shell
sudo cp /snap/authd-oidc/current/conf/authd/oidc.conf /etc/authd/brokers.d/
```

::::
:::::

This file is used to declare the brokers available on the system.

```{note}
Several brokers can be enabled at the same time.
```

## Application registration

This section demonstrates registering an OAuth 2.0 application that your chosen
broker can then use to authenticate users.

:::::{tab-set}
:sync-group: broker

::::{tab-item} Google IAM
:sync: google

To register a new application in Google IAM, go to the [Credentials page](https://console.cloud.google.com/apis/credentials).

Click {menuselection}`Create credentials --> OAuth client ID`.

![Menu showing selection of Create credentials > OAuth client ID.](../assets/google-app-registration.png)

Select the {guilabel}`TVs and Limited Input devices` application type.

![Menu showing app type.](../assets/google-choose-app-type.png)

Name your OAuth 2.0 client and click {guilabel}`Create`.

Your app's `Client ID` and `Client secret` will be shown on the page, store them
somewhere as you will need them in the next step.

![Screen showing app credentials.](../assets/google-app-credentials.png)

For more detailed information please refer to the [OAuth 2.0 for TV and
Limited-Input Device Applications documentation](https://developers.google.com/identity/protocols/oauth2/limited-input-device).


::::

::::{tab-item} Microsoft Entra ID
:sync: msentraid

Register a new application in the Microsoft Entra admin center. Once registered,
note the {guilabel}`Application (client) ID` and the
{guilabel}`Directory (tenant) ID` from the {guilabel}`Overview` menu. These IDs
correspond to the `<CLIENT_ID>` and `<ISSUER_ID>`, respectively, which are used
in the next section.

To register a new application, in Entra, select the menu
{menuselection}`Entra ID --> App registrations`:

![Menu showing selection of App registrations under Applications.](../assets/app-registration.png)

Then {guilabel}`New registration`:

![User interface showing selection of New registration in App registrations.](../assets/new-registration.png)

And configure it as follows:

![Configuration screen for the new registration.](../assets/configure-registration.png)

In {menuselection}`Manage --> API permissions`, set the following **Microsoft
Graph** permissions:

![Configuration screen for Microsoft Graph permissions.](../assets/graph-permissions.png)

Ensure the API permission type is set to **Delegated** for each permission.

The {guilabel}`GroupMember.Read.All` permission needs admin consent. Click on
{guilabel}`Grant admin consent for <TENANT_NAME>` to provide this consent.

Finally, as the supported authentication mechanism is the device workflow, you
need to allow the public client workflows. In {menuselection}`Manage -->
Authentication (Preview) --> Settings`, ensure that {guilabel}`Allow public
client flows` is set to **Enabled**.

[The Microsoft documentation](https://learn.microsoft.com/en-us/entra/identity-platform/quickstart-register-app)
provides detailed instructions for registering an application with the Microsoft
identity platform.

### Redirect URI

If you plan to use the device registration feature (see [configure device
registration](#configure-device-registration)), you need to configure a redirect
URI for the application. Go to {menuselection}`Manage --> Authentication
(Preview)`, click on {guilabel}`Add Redirect URI`, then choose {guilabel}`Mobile
and desktop applications` and select the following URI:

```
https://login.microsoftonline.com/common/oauth2/nativeclient
```

::::

::::{tab-item} Keycloak
:sync: keycloak

Register a new client in Keycloak. Go to {menuselection}`Manage --> Clients` and
create the client.

![Create client button being clicked.](../assets/keycloak-create-client.png)

Configure the client as follows:

1. General settings
    * Set the client type to {guilabel}`OpenID Connect`.
    * Pick a valid client ID, for example, `ubuntu-authd`.
      This corresponds to the `<CLIENT_ID>` which is used in the next section.
2. Capability config
    * (Optional) Enable client authentication
    * Enable the {guilabel}`OAuth 2.0 Device Authorization Grant` authentication
      flow.
3. Login settings
   * No need to change anything here.

Finally, click on {guilabel}`Save` to create the client.

If you enabled client authentication, find the client secret in the
{guilabel}`Credentials` tab. This corresponds to the `<CLIENT_SECRET>` in the
next section.

```{admonition} Set email addresses for users
:class: tip
Make sure that all users who should be able to log in through this broker have
an email address configured in Keycloak.
```

::::
:::::


## Broker configuration

Now we can configure the broker. Note that different brokers can require
different configuration data.

:::::{tab-set}
:sync-group: broker

::::{tab-item} Google IAM
:sync: google

To configure Google IAM, edit  `/var/snap/authd-google/current/broker.conf`:

```ini
[oidc]
issuer = https://accounts.google.com
client_id = <CLIENT_ID>
client_secret = <CLIENT_SECRET>
```
::::

::::{tab-item} Microsoft Entra ID
:sync: msentraid

To configure Entra ID, edit  `/var/snap/authd-msentraid/current/broker.conf`:

```ini
[oidc]
issuer = https://login.microsoftonline.com/<ISSUER_ID>/v2.0
client_id = <CLIENT_ID>
```
::::

::::{tab-item} Keycloak
:sync: keycloak

To configure the authd-oidc broker for Keycloak, edit
`/var/snap/authd-oidc/current/broker.conf`:

```ini
[oidc]
issuer = https://<host>/realms/<realm-name>
client_id = <CLIENT_ID>
```

If you enabled client authentication, you also need to add the client secret:

```ini
client_secret = <CLIENT_SECRET>
```

::::
:::::

(ref::config-force-provider-auth)=
## Force remote authentication with the identity provider

By default, remote authentication with the identity provider only happens if
there is a working internet connection and the provider is reachable during
login.

If you want to force remote authentication, even when the provider is
unreachable, enable it as follows:

```ini
[oidc]
...
force_provider_authentication = true
```

```{warning}
In some cases, this may prevent login, such as when there are network issues.
```

(ref::config-allowed-users)=
## Configure allowed users

The users who are allowed to log in (after successfully authenticating via the
identity provider) are configured in the `users` section of the
`/var/snap/authd-<broker_name>/current/broker.conf` file:

```ini
[users]
## 'allowed_users' specifies the users who are permitted to log in after
## successfully authenticating with the Identity Provider.
## Values are separated by commas. Supported values:
## - 'OWNER': Grants access to the user specified in the 'owner' option
##            (see below). This is the default.
## - 'ALL': Grants access to all users who successfully authenticate
##          with the Identity Provider.
## - <username>: Grants access to specific additional users
##               (e.g. user1@example.com).
## Example: allowed_users = OWNER,user1@example.com,admin@example.com
#allowed_users = OWNER

## 'owner' specifies the user assigned the owner role. This user is
## permitted to log in if 'OWNER' is included in the 'allowed_users'
## option.
##
## If this option is left unset, the first user to successfully log in
## via this broker will automatically be assigned the owner role. A
## drop-in configuration file will be created in broker.conf.d/ to set
## the 'owner' option.
##
## To disable automatic assignment, you can either:
## 1. Explicitly set this option to an empty value (e.g. owner = "")
## 2. Remove 'OWNER' from the 'allowed_users' option
##
## Example: owner = user2@example.com
#owner =
```

By default, the first person to log in to the machine is automatically
registered as the owner. If you wish to override this behavior then specify a
list of allowed users with the `allowed_users` option, while omitting the
`OWNER` keyword:

```text
allowed_users = person1@email.com,person2@email.com
```

Alternatively, you can directly register someone as the owner by using the `owner`
option:

```text
owner = your@email.com
```

Explicitly setting an empty owner, has the same effect as omitting the `OWNER`
keyword in `allowed_users`:

```text
owner = ""
```

:::::{admonition} Only my first logged-in user has access to the machine?
:class: important
By default, the first logged-in user is defined as the "owner" and only the
owner can log in.
To allow more users to log in, update the list of allowed users.

If an administrator is the first to log in to a machine and becomes the owner,
they can ensure that the next user to log in becomes the owner by removing the
`20-owner-autoregistration.conf` file:

::::{tab-set}
:sync-group: broker

:::{tab-item} Google IAM
:sync: google

```shell
sudo rm /var/snap/authd-google/current/broker.conf.d/20-owner-autoregistration.conf
```
:::

:::{tab-item} Microsoft Entra ID
:sync: msentraid

```shell
sudo rm /var/snap/authd-msentraid/current/broker.conf.d/20-owner-autoregistration.conf
```
:::

:::{tab-item} Keycloak
:sync: keycloak

```shell
sudo rm /var/snap/authd-oidc/current/broker.conf.d/20-owner-autoregistration.conf
```
:::
::::


This file is generated when a user logs in and becomes the owner. If it is
removed, it will be regenerated on the next successful login.

:::::

(ref::config-user-groups)=
## Configure user groups

Some brokers support adding users to groups that are configured in the identity
provider.

> See the [group management reference](reference::group-management) for more details.

In addition, you can configure extra groups for authd users.
On login, the users are added to these groups automatically.
Specify any extra groups in the `users` section of the broker
configuration file:

```ini
[users]
## A comma-separated list of local groups which authd users will be
## added to upon login.
## Example: extra_groups = users
#extra_groups =
```

There is also an `owner_extra_groups` option for specifying additional local
groups, to which only the user with the [owner role](#configure-allowed-users)
is added:

```ini
## Like 'extra_groups', but only the user assigned the owner role
## will be added to these groups.
## Example: owner_extra_groups = sudo,lpadmin
#owner_extra_groups =
```

(ref::device-registration)=
## Configure device registration

:::::{tab-set}
:sync-group: broker

::::{tab-item} Google IAM
:sync: google

The Google IAM broker does not support device registration.
::::

::::{tab-item} Microsoft Entra ID
:sync: msentraid

When using the Microsoft Entra ID broker, you can enable automatic device
registration, which allows administrators to manage registered devices in the
Microsoft Entra admin center.

Automatic device registration can be enabled with the `register_device`
option in the `msentraid` section of the broker configuration file:

```ini
[msentraid]
## Enable automatic device registration with Microsoft Entra ID
## when a user logs in through this broker.
##
## If set to true, authd will attempt to register the local machine
## as a device in Entra ID upon successful login.
##
## If set to false (the default), device registration will be skipped.
#register_device = false
```

```{admonition} Changing this option forces re-authentication
:class: note
When changing this option, users are forced to re-authenticate via device
authentication on the next login.
```

```{admonition} Set the redirect URI
:class: tip
Make sure that the application in the Microsoft Entra admin center has a
redirect URI configured as described in [Redirect URI](#redirect-uri).
```
::::

::::{tab-item} Keycloak
:sync: keycloak

The authd-oidc broker does not support device registration.
::::
:::::

## Restart the broker

When a configuration file is added you have to restart authd:

```shell
sudo systemctl restart authd
```

When the configuration of a broker is updated, you also have to restart the
broker:

:::::{tab-set}
:sync-group: broker

::::{tab-item} Google IAM
:sync: google

```shell
sudo snap restart authd-google
```

::::

::::{tab-item} Microsoft Entra ID
:sync: msentraid

```shell
sudo snap restart authd-msentraid
```

::::

::::{tab-item} Keycloak
:sync: keycloak

```shell
sudo snap restart authd-oidc
```

::::
:::::

## Configure login timeout

By default on Ubuntu, the login timeout is 60s.

This may be too brief for a device code flow authentication.

It can be modified by changing the value of `LOGIN_TIMEOUT` in `/etc/login.defs`.

## Configure the authd service

The authd service is configured in `/etc/authd/authd.yaml`.

This provides configuration options for logging verbosity and UID/GID ranges.

(ref::config-pwquality)=
## Configure password quality

You can change authd's local password policy to ensure that users always set
strong passwords.

If your mobile device management (MDM) solution includes a compliance check for
the passwords of authd users, you may also need to configure authd's password
policy so that it matches that of the MDM.

authd depends on the libpwquality library, which supports configuring password
quality.

To configure the local password policy for authd, create a drop file in
`/etc/security/pwquality.conf.d/`.
This will override the default values in `/etc/security/pwquality.conf`.

First, create a directory for the custom configuration file:

```shell
sudo mkdir -p /etc/security/pwquality.conf.d/
```

Then edit the file to add your settings.

```shell
sudoedit /etc/security/pwquality.conf.d/99_custom-options.conf
```

For example, if your policy requires that passwords have a 14 character minimum,
edit the drop file to set the value for `minlen`:

```{code-block} diff
:caption: /etc/security/pwquality.conf.d/99_custom-options.conf
# This file overrides the defaults set in /etc/security/pwquality.conf
# Refer to the pwquality file for additional configuration options.
# Minimum acceptable size for the new password (plus one if
# credits are not disabled which is the default). (See pam_cracklib manual.)
# Cannot be set to lower value than 6 (default is 8).
minlen = 14
```

> The [man pages](https://manpages.ubuntu.com/manpages/noble/man5/pwquality.conf.5.html)
provide a full list of configuration options for the libpwquality library.
