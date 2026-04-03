#!/bin/bash

set -euo pipefail

# ==============================================================================
# SpeedUP - One-Shot Installer for Debian/Ubuntu
# This script installs the Puck dedicated server, SteamCMD, and the SpeedUP
# web-based administration panel.
#
# Compatibility choice:
# - Keep legacy internal deployment names for compatibility.
# - Keep visible branding as SpeedUP.
# ==============================================================================

PANEL_REPO_RAW_URL="${SPEEDUP_REPO_RAW_URL:-${PUCKERUP_REPO_RAW_URL:-https://raw.githubusercontent.com/pogsee/PuckerUp/main/app}}"
INSTALL_DIR="/srv/PuckerUp"
SERVICE_NAME="puckerup"
MAIN_BINARY="puckerup"
PASSWORD_BINARY="puckerup-passwd"
PANEL_FILES=("index.html" "login.html" "dashboard.html" "$MAIN_BINARY" "$PASSWORD_BINARY")
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_APP_DIR="$SCRIPT_DIR/app"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

print_heading() {
    echo -e "\n${YELLOW}=======================================================================${NC}"
    echo -e "${YELLOW} $1${NC}"
    echo -e "${YELLOW}=======================================================================${NC}"
}

check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        echo -e "${RED}Error: This script must be run as root. Please use 'sudo'.${NC}"
        exit 1
    fi
}

download_panel_file() {
    local file_name="$1"
    local destination="$INSTALL_DIR/$file_name"
    local source_url="$PANEL_REPO_RAW_URL/$file_name"

    if ! wget -qO "$destination" "$source_url"; then
        rm -f "$destination"
        echo -e "${RED}Error: Failed to download '$file_name' from $source_url${NC}"
        exit 1
    fi

    if [ ! -s "$destination" ]; then
        echo -e "${RED}Error: Failed to download '$file_name' from $source_url${NC}"
        exit 1
    fi
}

has_local_source_tree() {
    local required_files=("build.sh" "main.go" "generate-password.go" "index.html" "login.html" "dashboard.html")
    local file_name

    for file_name in "${required_files[@]}"; do
        if [ ! -f "$SCRIPT_DIR/$file_name" ]; then
            return 1
        fi
    done

    return 0
}

has_local_panel_package() {
    local file_name

    for file_name in "${PANEL_FILES[@]}"; do
        if [ ! -s "$LOCAL_APP_DIR/$file_name" ]; then
            return 1
        fi
    done

    return 0
}

ensure_go_toolchain() {
    if command -v go >/dev/null 2>&1; then
        return 0
    fi

    print_heading "Installing Go Toolchain For Local Build"
    apt install -y golang-go
}

prepare_local_panel_package() {
    if has_local_panel_package; then
        return 0
    fi

    if ! has_local_source_tree; then
        return 1
    fi

    ensure_go_toolchain

    print_heading "Building Local SpeedUP Package"
    (
        cd "$SCRIPT_DIR"
        ./build.sh
    )

    has_local_panel_package
}

install_local_panel_files() {
    local file_name

    mkdir -p "$INSTALL_DIR"

    for file_name in "${PANEL_FILES[@]}"; do
        if [[ "$file_name" == *.html ]]; then
            install -m 0644 "$LOCAL_APP_DIR/$file_name" "$INSTALL_DIR/$file_name"
        else
            install -m 0755 "$LOCAL_APP_DIR/$file_name" "$INSTALL_DIR/$file_name"
        fi
    done
}

install_panel_files_from_dir() {
    local source_dir="$1"
    local file_name

    mkdir -p "$INSTALL_DIR"

    for file_name in "${PANEL_FILES[@]}"; do
        if [[ "$file_name" == *.html ]]; then
            install -m 0644 "$source_dir/$file_name" "$INSTALL_DIR/$file_name"
        else
            install -m 0755 "$source_dir/$file_name" "$INSTALL_DIR/$file_name"
        fi
    done
}

download_remote_panel_package() {
    local temp_dir
    local file_name
    local source_url

    temp_dir="$(mktemp -d)"
    trap 'rm -rf "$temp_dir"' RETURN

    for file_name in "${PANEL_FILES[@]}"; do
        source_url="$PANEL_REPO_RAW_URL/$file_name"
        echo "Downloading $file_name..."
        if ! wget -qO "$temp_dir/$file_name" "$source_url"; then
            rm -f "$temp_dir/$file_name"
            echo -e "${RED}Error: Failed to download '$file_name' from $source_url${NC}"
            echo -e "${RED}The configured package source is incomplete. Provide a full local project/app package or set SPEEDUP_REPO_RAW_URL to a package that contains all runtime files.${NC}"
            exit 1
        fi

        if [ ! -s "$temp_dir/$file_name" ]; then
            echo -e "${RED}Error: Failed to download '$file_name' from $source_url${NC}"
            echo -e "${RED}The configured package source is incomplete. Provide a full local project/app package or set SPEEDUP_REPO_RAW_URL to a package that contains all runtime files.${NC}"
            exit 1
        fi
    done

    install_panel_files_from_dir "$temp_dir"
}

generate_random_alnum() (
    set +o pipefail
    tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 12
)

clear
echo -e "${GREEN}Welcome to the SpeedUP Installer!${NC}"
echo ""
echo "This script will perform the following actions:"
echo " - Update your system and install necessary dependencies."
echo " - Create a swap file to ensure stable performance."
echo " - Install SteamCMD and the Puck server files."
echo " - Create a low-privilege 'puck' user to run the game servers."
echo " - Set up systemd services to manage the game servers."
echo " - Download and install the SpeedUP admin panel."
echo " - Set up a systemd service to ensure SpeedUP runs on boot."
echo ""
echo "Compatibility mode: runtime paths remain under /srv/PuckerUp and binary names remain puckerup/puckerup-passwd."
echo "Visible panel branding remains SpeedUP."
echo ""
read -p "Do you wish to continue with installation? (y/n): " confirm
if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
    echo "Installation cancelled."
    exit 0
fi

check_root

print_heading "Updating System and Installing Dependencies"
apt update && apt upgrade -y
apt install -y software-properties-common curl wget

print_heading "Creating 500MB Swap File"
if [ ! -f /swapfile ]; then
    fallocate -l 500M /swapfile
    dd if=/dev/zero of=/swapfile bs=1M count=500
    chmod 600 /swapfile
    mkswap /swapfile
    swapon /swapfile
    echo '/swapfile none swap sw 0 0' | tee -a /etc/fstab
    echo -e "${GREEN}Swap file created and enabled.${NC}"
else
    echo -e "${YELLOW}Swap file already exists. Skipping creation.${NC}"
fi

print_heading "Configuring Repositories for SteamCMD"
dpkg --add-architecture i386
apt update

print_heading "Installing SteamCMD"
echo steam steam/question select "I AGREE" | debconf-set-selections
echo steam steam/license note '' | debconf-set-selections
apt install -y steamcmd

print_heading "Installing Puck Dedicated Server"
mkdir -p /srv/puckserver

/usr/games/steamcmd +force_install_dir /srv/puckserver +login anonymous +app_info_update 1 +quit || true
/usr/games/steamcmd +force_install_dir /srv/puckserver +login anonymous +app_update 3481440 validate +quit

print_heading "Creating 'puck' System User"
if ! id "puck" &>/dev/null; then
    useradd -r -s /bin/false puck
    echo -e "${GREEN}'puck' user created.${NC}"
else
    echo -e "${YELLOW}'puck' user already exists.${NC}"
fi
chown -R puck:puck /srv/puckserver

print_heading "Creating Puck Server Systemd Service"
cat > /etc/systemd/system/puck@.service << 'EOF'
[Unit]
Description=Puck Dedicated Server (Instance %i)
After=network.target

[Service]
WorkingDirectory=/srv/puckserver
User=puck
Group=puck
ExecStart=/srv/puckserver/Puck.x86_64 --serverConfigurationPath %i.json
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload

print_heading "Generating Initial Server Config Files"
GAME_PASSWORD="$(generate_random_alnum)"

for i in {1..4}; do
    port=$((7777 + (i-1)*2))
    pingPort=$((7778 + (i-1)*2))
    cat > "/srv/puckserver/server${i}.json" <<EOF
{
  "port": ${port},
  "pingPort": ${pingPort},
  "name": "Puck Server ${i}",
  "maxPlayers": 10,
  "password": "${GAME_PASSWORD}",
  "voip": false,
  "isPublic": true,
  "adminSteamIds": [],
  "reloadBannedSteamIds": true,
  "usePuckBannedSteamIds": true,
  "printMetrics": true,
  "kickTimeout": 1800,
  "sleepTimeout": 900,
  "joinMidMatchDelay": 10,
  "targetFrameRate": 380,
  "serverTickRate": 360,
  "clientTickRate": 360,
  "startPaused": false,
  "allowVoting": true,
  "phaseDurationMap": {"Warmup":600,"FaceOff":3,"Playing":300,"BlueScore":5,"RedScore":5,"Replay":10,"PeriodOver":15,"GameOver":15},
  "mods": [
    {"id": 3497097214, "enabled": true, "clientRequired": false},
    {"id": 3497344177, "enabled": true, "clientRequired": false},
    {"id": 3503065207, "enabled": true, "clientRequired": true}
  ]
}
EOF
done
chown puck:puck /srv/puckserver/*.json
echo -e "${GREEN}Default config files created for servers 1-4.${NC}"

print_heading "Downloading and Installing SpeedUP Admin Panel"

if has_local_panel_package; then
    echo -e "${GREEN}Using local packaged panel files from $LOCAL_APP_DIR.${NC}"
    install_local_panel_files
elif has_local_source_tree; then
    if ! prepare_local_panel_package; then
        echo -e "${RED}Error: Local source checkout detected, but the packaged runtime files are incomplete and the local build did not produce a full app directory.${NC}"
        echo -e "${RED}Expected files: ${PANEL_FILES[*]}${NC}"
        exit 1
    fi

    echo -e "${GREEN}Using local packaged panel files from $LOCAL_APP_DIR.${NC}"
    install_local_panel_files
elif [ -d "$LOCAL_APP_DIR" ]; then
    echo -e "${RED}Error: Found a local app directory at $LOCAL_APP_DIR, but it is incomplete.${NC}"
    echo -e "${RED}Expected runtime files: ${PANEL_FILES[*]}${NC}"
    echo -e "${RED}Run ./build.sh first or upload a complete packaged app directory with both binaries included.${NC}"
    exit 1
else
    download_remote_panel_package
fi

chmod +x "$INSTALL_DIR/$MAIN_BINARY"
chmod +x "$INSTALL_DIR/$PASSWORD_BINARY"

print_heading "Generating Admin Panel Password"
ADMIN_PASSWORD=$("$INSTALL_DIR/$PASSWORD_BINARY")

print_heading "Creating SpeedUP Systemd Service"
cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=SpeedUP Admin Panel Web Server
After=network.target

[Service]
User=root
Group=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/$MAIN_BINARY
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}.service"
systemctl restart "${SERVICE_NAME}.service" || systemctl start "${SERVICE_NAME}.service"
echo -e "${GREEN}SpeedUP service created and started.${NC}"

mkdir -p /home/puck/.steam/sdk64
ln -sf /srv/puckserver/steamclient.so /home/puck/.steam/sdk64/steamclient.so

IP_ADDRESS=$(hostname -I | awk '{print $1}')
print_heading "Installation Complete!"
echo -e "You can now access the SpeedUP admin panel in your web browser."
echo ""
echo -e "    SpeedUP URL: ${YELLOW}http://${IP_ADDRESS}:8080${NC}"
echo -e "    SpeedUP password: ${GREEN}${ADMIN_PASSWORD}${NC}"
echo -e "    SAVE THE ABOVE PASSWORD. It cannot be changed or displayed again."
echo ""
echo -e "The randomly generated password for your actual puck servers is:"
echo -e "    Puck Server Password: ${GREEN}${GAME_PASSWORD}${NC}"
echo ""
echo -e "Installed runtime files:"
for file_name in "${PANEL_FILES[@]}"; do
    echo -e "    ${INSTALL_DIR}/${file_name}"
done
echo ""
echo -e "Thank you for using SpeedUP!"#!/bin/bash

# ==============================================================================
# SpeedUP - One-Shot Installer for Debian/Ubuntu
# This script installs the Puck dedicated server, SteamCMD, and the SpeedUP
# web-based administration panel.
# ==============================================================================

# --- Configuration ---
# The GitHub repository where the SpeedUP application files are stored.
# The script will download the files from the /app subdirectory.
PUCKERUP_REPO_RAW_URL="https://raw.githubusercontent.com/pogsee/PuckerUp/main/app"

# --- Style Definitions ---
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# --- Script Functions ---

# Function to print a formatted heading.
print_heading() {
    echo -e "\n${YELLOW}=======================================================================${NC}"
    echo -e "${YELLOW} $1${NC}"
    echo -e "${YELLOW}=======================================================================${NC}"
}

# Function to check if the script is being run as root.
check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        echo -e "${RED}Error: This script must be run as root. Please use 'sudo'.${NC}"
        exit 1
    fi
}

# --- Main Script Logic ---

# 1. Introduction and Confirmation
clear
echo -e "${GREEN}Welcome to the SpeedUP Installer!${NC}"
echo ""
echo "This script will perform the following actions:"
echo " - Update your system and install necessary dependencies."
echo " - Create a swap file to ensure stable performance."
echo " - Install SteamCMD and the Puck server files."
echo " - Create a low-privilege 'puck' user to run the game servers."
echo " - Set up systemd services to manage the game servers."
echo " - Download and install the SpeedUP admin panel."
echo " - Set up a systemd service to ensure SpeedUP runs on boot."
echo ""
echo "If you only want the panel, choose no and adapt the manual instructions at https://github.com/pogsee/PuckerUp"
echo ""
read -p "Do you wish to continue with installation? (y/n): " confirm
if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
    echo "Installation cancelled."
    exit 0
fi

# Set the script to exit immediately if any command fails.
set -e

# 2. Check for Root Privileges
check_root

# 3. System Setup and Dependency Installation
print_heading "Updating System and Installing Dependencies"
apt update && apt upgrade -y
apt install -y software-properties-common curl wget

print_heading "Creating 500MB Swap File"
if [ ! -f /swapfile ]; then
    fallocate -l 500M /swapfile
    dd if=/dev/zero of=/swapfile bs=1M count=500
    chmod 600 /swapfile
    mkswap /swapfile
    swapon /swapfile
    echo '/swapfile none swap sw 0 0' | tee -a /etc/fstab
    echo -e "${GREEN}Swap file created and enabled.${NC}"
else
    echo -e "${YELLOW}Swap file already exists. Skipping creation.${NC}"
fi

print_heading "Configuring Repositories for SteamCMD"
dpkg --add-architecture i386
apt update

print_heading "Installing SteamCMD"
echo steam steam/question select "I AGREE" | debconf-set-selections
echo steam steam/license note '' | debconf-set-selections
apt install -y steamcmd

# 4. Puck Server Installation
print_heading "Installing Puck Dedicated Server"
mkdir -p /srv/puckserver

# PASS 1: Update the App Info and structure (ignoring errors)
/usr/games/steamcmd +force_install_dir /srv/puckserver +login anonymous +app_info_update 1 +quit || true

# PASS 2: Install the server
/usr/games/steamcmd +force_install_dir /srv/puckserver +login anonymous +app_update 3481440 validate +quit

print_heading "Creating 'puck' System User"
if ! id "puck" &>/dev/null; then
    useradd -r -s /bin/false puck
    echo -e "${GREEN}'puck' user created.${NC}"
else
    echo -e "${YELLOW}'puck' user already exists.${NC}"
fi
chown -R puck:puck /srv/puckserver

print_heading "Creating Puck Server Systemd Service"
cat > /etc/systemd/system/puck@.service << 'EOF'
[Unit]
Description=Puck Dedicated Server (Instance %i)
After=network.target

[Service]
WorkingDirectory=/srv/puckserver
User=puck
Group=puck
# The game server binary requires a start_server.sh script to run.
# This is a common pattern for Unity games.
ExecStart=/srv/puckserver/Puck.x86_64 --serverConfigurationPath %i.json
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload

print_heading "Generating Initial Server Config Files"
# Generate a random password for the game servers themselves.
GAME_PASSWORD=$(tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 12)

for i in {1..4}; do
    port=$((7777 + (i-1)*2))
    pingPort=$((7778 + (i-1)*2))
    cat > "/srv/puckserver/server${i}.json" <<EOF
{
  "port": ${port},
  "pingPort": ${pingPort},
  "name": "Puck Server ${i}",
  "maxPlayers": 10,
  "password": "${GAME_PASSWORD}",
  "voip": false,
  "isPublic": true,
  "adminSteamIds": [],
  "reloadBannedSteamIds": true,
  "usePuckBannedSteamIds": true,
  "printMetrics": true,
  "kickTimeout": 1800,
  "sleepTimeout": 900,
  "joinMidMatchDelay": 10,
  "targetFrameRate": 380,
  "serverTickRate": 360,
  "clientTickRate": 360,
  "startPaused": false,
  "allowVoting": true,
  "phaseDurationMap": {"Warmup":600,"FaceOff":3,"Playing":300,"BlueScore":5,"RedScore":5,"Replay":10,"PeriodOver":15,"GameOver":15},
  "mods": [
    {"id": 3497097214, "enabled": true, "clientRequired": false},
    {"id": 3497344177, "enabled": true, "clientRequired": false},
    {"id": 3503065207, "enabled": true, "clientRequired": true}
  ]
}
EOF
done
chown puck:puck /srv/puckserver/*.json
echo -e "${GREEN}Default config files created for servers 1-4.${NC}"

# 5. SpeedUP Admin Panel Installation
print_heading "Downloading and Installing SpeedUP Admin Panel"
mkdir -p /srv/PuckerUp
wget -qO /srv/PuckerUp/index.html "${PUCKERUP_REPO_RAW_URL}/index.html"
wget -qO /srv/PuckerUp/dashboard.html "${PUCKERUP_REPO_RAW_URL}/dashboard.html"
wget -qO /srv/PuckerUp/login.html "${PUCKERUP_REPO_RAW_URL}/login.html"
wget -qO /srv/PuckerUp/puckerup "${PUCKERUP_REPO_RAW_URL}/puckerup"
wget -qO /srv/PuckerUp/puckerup-passwd "${PUCKERUP_REPO_RAW_URL}/puckerup-passwd"
echo -e "${GREEN}SpeedUP files downloaded.${NC}"

chmod +x /srv/PuckerUp/puckerup
chmod +x /srv/PuckerUp/puckerup-passwd

# 6. Generate SpeedUP Admin Password
print_heading "Generating Admin Panel Password"
ADMIN_PASSWORD=$(/srv/PuckerUp/puckerup-passwd)

# 7. Setup SpeedUP Systemd Service
print_heading "Creating SpeedUP Systemd Service"
cat > /etc/systemd/system/puckerup.service << 'EOF'
[Unit]
Description=SpeedUP Admin Panel Web Server
After=network.target

[Service]
User=root
Group=root
WorkingDirectory=/srv/PuckerUp
ExecStart=/srv/PuckerUp/puckerup
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable puckerup.service
systemctl start puckerup.service
echo -e "${GREEN}SpeedUP service created and started.${NC}"

# steam being weird and not seeing steamclient.so in local directory, no idea why
mkdir -p /home/puck/.steam/sdk64
ln -s /srv/puckserver/steamclient.so /home/puck/.steam/sdk64/steamclient.so

# 8. Final Instructions
IP_ADDRESS=$(hostname -I | awk '{print $1}')
print_heading "Installation Complete!"
echo -e "You can now access the SpeedUP admin panel in your web browser."
echo ""
echo -e "    SpeedUP URL: ${YELLOW}http://${IP_ADDRESS}:8080${NC}"
echo -e "    SpeedUP password: ${GREEN}${ADMIN_PASSWORD}${NC}"
echo -e "    SAVE THE ABOVE PASSWORD. It cannot be changed or displayed again."
echo ""
echo -e "The randomly generated password for your actual puck servers is:"
echo -e "    Puck Server Password: ${GREEN}${GAME_PASSWORD}${NC}"
echo ""
echo -e "Thank you for using SpeedUP!"
