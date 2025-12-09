# aarg - Another APT Repository Generator

A modern tool for aggregating Debian packages from multiple sources into opinionated, unified, signed APT repositories with some more optional features.

## Notable Features

- **Multi-Source Aggregation**: Combine packages from GitHub Releases, OpenBuildService (OBS), and existing APT repositories (to be expanded)
- **Automatic Version Retention**: Flexible retention policies to only keep newest versions according to pattern
- **Debug and Source Packages**: Automatic inclusion of debug and source packages if selected
- **Redirected Pool**: Generate repository metadata space efficient without hosting package files, pool requests are redirected to original feed URLs
- **Composable Pipeline**: Each step (fetch, generate, publish) can run independently and be integrated with other tools

## Installation

### Using go install

```bash
go install github.com/dionysius/aarg@latest
```

## Quick Start

### Create Configuration

Create a main configuration file and repository configuration file(s), refer to [examples](examples) which contain detailed comments for various options.

### Build Repository

```bash
# Complete build: fetch packages, generate repo, publish
aarg build --all

# Or run steps individually:
aarg fetch --all      # Download packages
aarg generate --all   # Generate APT metadata

# Serve or publish result
aarg serve            # Serve locally
aarg publish          # Upload to provider
```

## Structure and Pipeline

Directories can also be customized in the config file.

```bash
/configured/root/
├── downloads/          # Where packages are downloaded by `fetch`
├── trusted/            # Verified packages are hardlinked here by `fetch`
└── public/             # Where repository indexes are created by `generate`
    ├── myrepo1/...     # (Standard repository structure inside)
    ├── ...
    └── index.html      # Optionally with web page using compose `web`
```

And `publish` would upload the `public` dir to selected provider.

## Disclaimer

Why another apt repository generator? Generating the apt indexes is actually the easy part once debian format parsing is available. How packages are collected, retained and organized was the main effort with a sprinkle of opinionated modifications. Offering the pool redirect feature with sensible wildcard redirects is the main invasion to the index generation. If you'd change the index files afterwards, you'd need to update the hashes and sign the release again anyway.

This project is hacked together during a holiday with help of AI. This is already the second iteration as AI tends to only add code and this makes it hard to maintain. Could also be related to prompt engineering and the model used. This version is a bit more authored but got a bit neglegted towards the end as I wanted to publish the first release. Will need some more work to make it more tidy.

### TODO's

Besides cleanup, better interfacing and testing:

- Remove staging and instead cleanup public directory after generation so it doesn't need to be symlinked anymore
- Internal server should also understand redirect metadata and offer redirects
- Add more potential sources and providers
- Offer package webpage describing metadata of each package, extract man pages, licenses, etc. The usual package description page from your known distribution.
- Make web template generation customizable
- Generated install script is too opinionated by using the OS' codename as suite in the sources configuration
- When using build, do publish only if changes are detected (or enforce with flag)
- And many things I just forgot
