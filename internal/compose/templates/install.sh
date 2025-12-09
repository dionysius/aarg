#!/bin/bash
# Automated installer for {{.RepoName}} APT repository

set -euo pipefail

# Available distributions for this repository
AVAILABLE_DISTRIBUTIONS=({{range .Distributions}}"{{.}}" {{end}})

# Parse arguments
INCLUDE_DEBUG=false
INCLUDE_SOURCE=false
VERSION_CODENAME=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --debug) INCLUDE_DEBUG=true; shift ;;
        --source) INCLUDE_SOURCE=true; shift ;;
        --dist)
            if [[ -n "$2" && "$2" != --* ]]; then
                VERSION_CODENAME="$2"
                shift 2
            else
                echo "Error: --dist requires a distribution codename argument"
                exit 1
            fi
            ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# If distro not specified, auto-detect
if [ -z "$VERSION_CODENAME" ]; then
    if [ ! -f /etc/os-release ]; then
        echo "Error: Cannot detect distribution (missing /etc/os-release)"
        exit 1
    fi

    source /etc/os-release
fi

# Check if detected or set distribution is available
if [[ ! " ${AVAILABLE_DISTRIBUTIONS[@]} " =~ " ${VERSION_CODENAME} " ]]; then
    echo "Error: Detected distribution '$VERSION_CODENAME' is not available in this repository"
    echo ""
    echo "Available distributions:"
    for dist in "${AVAILABLE_DISTRIBUTIONS[@]}"; do
        echo "  - $dist"
    done
    echo ""
    echo "You can specify a distribution manually with: --dist <codename>"
    exit 1
fi

# Check for required dependencies
MISSING_DEPS=()
if ! command -v curl &> /dev/null; then
    MISSING_DEPS+=("curl")
fi
if ! dpkg -l apt-transport-https 2>/dev/null | grep -q ^ii; then
    MISSING_DEPS+=("apt-transport-https")
fi

if [ ${#MISSING_DEPS[@]} -gt 0 ]; then
    echo "Missing required dependencies: ${MISSING_DEPS[*]}. Installing..."
    sudo apt-get update
    sudo apt-get install -y "${MISSING_DEPS[@]}"
fi

# Install repository signing key
echo "Installing repository signing key..."
KEYRING_DIR="/etc/apt/keyrings"
sudo mkdir -p "$KEYRING_DIR"

# Use keyring file for this repository
SIGNED_BY="$KEYRING_DIR/{{.KeyringName}}.gpg"
curl -fsSL {{.BaseURL}}/keys/signing-key.gpg | sudo tee "$SIGNED_BY" > /dev/null

# Create repository sources file
SOURCES_FILE="/etc/apt/sources.list.d/{{.RepoName}}.sources"
echo "Creating $SOURCES_FILE..."

# Build components list
COMPONENTS="main"
if [ "$INCLUDE_DEBUG" = true ]; then
    COMPONENTS="$COMPONENTS debug"
fi

# Build types list
TYPES="deb"
if [ "$INCLUDE_SOURCE" = true ]; then
    TYPES="$TYPES deb-src"
fi

sudo tee "$SOURCES_FILE" > /dev/null <<EOF
Types: $TYPES
URIs: {{.BaseURL}}/{{.RepoName}}
Suites: $VERSION_CODENAME
Components: $COMPONENTS
Signed-By: $SIGNED_BY
EOF

# Update package lists
echo "Updating package lists..."
sudo apt-get update

echo "Successfully installed {{.RepoName}} repository! You can now install packages using: apt install <package-name>"
