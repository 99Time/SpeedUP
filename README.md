# SpeedUP
A web admin panel for administrating Puck servers, with an HTML frontend and a Go backend.

## Complete server installation (Installs SteamCMD, Puck server, and SpeedUP)
Note, only tested with Ubuntu 24.04. Workarounds are needed for Debian 13 to install SteamCMD. 

Also, if you already have server configs under /srv/puckserver, please back these up, the script will overwrite them. I did this to get new hosters started with a usable config. You can always comment these lines if you want - the panel will handle existing configs.

-Grab the script with `wget https://raw.githubusercontent.com/pogsee/PuckerUp/main/PuckerUp.sh`

-Make it executable with `chmod +x PuckerUp.sh`

-Run the script with `./PuckerUp.sh`

The script will present you with a URL to access, and your passwords. Save the SpeedUP password as it will not be shown again.

## Manual installation
Not happening yet - there are still hardcoded paths across parts of the stack. You are welcome to edit the Go files and use build.sh, or adapt the installer script to your environment.

## Instructions/notes
Not much to it - login at the URL, set what you want to set, and save the changes. Restart the server for changes to take effect.

There is bruteforce protection built in, wrong password 5 times will result in an IP block for 10 minutes.

You can change the panel password by logging into the server and executing `/srv/PuckerUp/puckerup-passwd`. Note this will not log out existing sessions immediately.

The panel now includes a dedicated dashboard page, server status aggregation, and player MMR browsing.

The Daily Scheduled Restart option creates a file under /srv/puckserver/schedules.json

## Dashboard and API
- `GET /api/servers/status` returns total online players and per-server occupancy using the existing `server*.json` config files plus optional runtime status JSON files when present.
- `GET /api/players/mmr` reads player records from `/srv/puckserver/UserData`, sorts by MMR by default, and supports `search` and `sort` query parameters.
- The dashboard page is served from `/dashboard.html` and refreshes automatically every 15 seconds.

### Runtime status files
If you want live player counts and player lists per server, drop a JSON file next to each server config using one of these names:
- `/srv/puckserver/server1-status.json`
- `/srv/puckserver/server1_status.json`
- `/srv/puckserver/server1.status.json`
- `/srv/puckserver/server1/status.json`

Supported fields are intentionally simple:

```json
{
	"playersOnline": 4,
	"maxPlayers": 10,
	"players": [
		{ "steamId": "76561198000000000", "name": "Player One" }
	]
}
```

## Credits
Gafurix for the cool game https://steamcommunity.com/app/2994020
VotePause and VoteForfeit mods by https://github.com/ViliamVadocz/
Crash Exploit Fix by https://github.com/ckhawks

## Example screenshot

<img width="995" height="951" alt="Screenshot 2025-10-19 at 17 35 03" src="https://github.com/user-attachments/assets/30257e6b-c587-4d1e-93a1-55201293cbe0" />
<img width="964" height="825" alt="Screenshot 2025-10-19 at 17 39 39" src="https://github.com/user-attachments/assets/76909865-3d8b-44ba-b5de-15f2289ef9c8" />

