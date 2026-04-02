---
myst:
  html_meta:
    "description lang=en":
      "authd reference pages covering supported providers, troubleshooting, groups management, and deployment."
---

(reference)=

# Reference

These guides provide technical information about authd.

## Providers

Multiple identity providers and brokers are supported by authd:

```{toctree}
:titlesonly:

Identity providers that authd supports <identity-providers>
```

## Troubleshooting

The documentation includes several pages that are helpful when troubleshooting
authd:

```{toctree}
:titlesonly:

Troubleshooting <troubleshooting>
```

## Groups

Managing groups of users that need the same access and permissions to resources
is supported by the Microsoft Entra ID broker for authd:

```{toctree}
:titlesonly:

Group management <group-management>
```
## Deployment

Deploying authd at scale can be achieved with Landscape or cloud-init.
The documentation includes snippets to get you started.

```{toctree}
:titlesonly:

Deploying and configuring authd with Landscape <landscape-deploy>
Deploying and configuring authd with cloud-init <cloud-init-deploy>
```

## Command line tool (authctl)

The `authctl` command line tool is used to manage authd users and groups.

Available commands:

```{toctree}
:titlesonly:

Command line tool (authctl) <cli/index>
```
