# Changelog

What changed in each Arrmada release, written for the people running it. Read **Before you
update** first: it lists anything that behaves differently afterwards, or that you need to do
by hand.

Arrmada is pre-1.0 (v0.x), so a minor version can change behaviour. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions are
[semantic](https://semver.org/). How to update:
[Updating Arrmada](https://github.com/tristenlammi/Arrmada/blob/main/docs/updating.md).

## [0.3.2] - 2026-10-20

### Before you update

- **Check your TV folders for episodes in the wrong place (#56).** v0.3.0 and v0.3.1 could
  put new episodes and upgrades of a show whose season folders aren't named "Season N"
  (`S01`, `Stagione 1`, `Saison 1`…) into a folder named like a season right inside the TV
  folder, such as `<TV folder>/S13/Season 12/`, instead of the show's own folder. This
  release stops that. To put files back by hand:
  1. Look directly inside each TV folder for folders named like a season (`S01`, `S13`,
     `Stagione 2`…). The files in them are named after their show.
  2. Move each file into its show's own season folder. If that episode is already there,
     keep the copy you want and delete the other.
  3. On the show's page, use **Refresh & rescan**. Delete the season-named folders once
     they're empty.
  4. If every file of a show was moved, delete the show in Arrmada **keeping its files**,
     put the files back, and add it again with a library scan.

  The next release adds **Put misplaced files back** to the show's page, which does this for
  you with a preview, and a health check listing the shows affected.

### Fixed

- **TV episodes no longer import into the wrong folder when season folders aren't named
  "Season N"** (#56). A show's folder is now found from the TV folder it's in, so `S01`,
  `Stagione 1`, `Saison 1`, `Staffel 1`, `Temporada 1`, `Series 1`, `Season.01` and your own
  season folder template all work, and shows named like a number ("1992", "24") stay
  themselves. New episodes join the season folder the show already has (`S13`) rather than
  a new `Season 13` beside it.
- **An upgrade no longer makes a second folder for a show** whose season folders are named
  `S01` (for example by an `S{season00}` season folder template). This was the other half of
  #56, seen with The Pitt.
- **Arrmada never imports into a folder other shows' files are in, or one named like a
  season.** It used to warn in the log and import anyway. Now the download waits in
  Activity → Needs you saying "Can't tell which folder *the show* lives in". Rescan, Rename and
  the duplicate check refuse such a folder too.

## [0.3.1] - 2026-10-18

### Highlights

- **Two-factor sign-in** (TOTP) for password accounts (#21).
- **Personal API keys** for scripts, n8n and Home Assistant.
- **Display titles**: show films and shows in English, in their original language, or in
  yours (#27).
- **Command palette**: Ctrl K / ⌘K searches titles, pages and settings from anywhere (#17).
- **Book naming templates** with a safe rename for books already in the library (#25).
- **A downloads folder per library folder**, so libraries on separate disks hardlink
  instead of copying (#11).

### Before you update

- **Rolling back to v0.3.0 needs `./update.sh --rollback --with-db`.** This version adds
  database migrations 0329 to 0333, so v0.3.0 refuses to start on the upgraded database.
- **Send to Kindle email moved.** The "Outgoing email" card under Settings → Alerts is now
  "Send to Kindle email" in Settings → Book server (the renamed audiobook server section).
  It only ever sent ebooks to Kindles, never alerts (#10). Old links still land on it.
- **Arrmada no longer creates library folders by itself at startup.** A missing folder may
  be a share that isn't mounted yet. New Compose installs get their default folders from
  the media setup container. On an existing install, use **Create it** on the folder in
  Settings → Library. It refuses a folder that held files, because its share is probably
  not mounted (#24).
- **qBittorrent downloads with their own save path turn off its Automatic Torrent
  Management**, so re-filing a torrent never moves its files.
- **The Unraid template now installs the all-in-one image** (`:stable-aio`, with
  qBittorrent and FlareSolverr built in). Containers installed from the earlier template
  keep running `:stable` until you change the Repository
  ([Unraid → Updating](https://github.com/tristenlammi/Arrmada/blob/main/docs/unraid.md#updating)).

### New

- **Two-factor sign-in (TOTP)** on the Me page: QR code setup, ten recovery codes, and
  "trust this device for 30 days". Admins can reset someone's two-factor and require it for
  admins. The `reset-2fa` command helps a locked-out owner. Repeated wrong codes raise an
  alert (#21).
- **Personal API keys** on the Me page, read-only or full, with optional expiry. A key can
  never do more than its owner's current role. Keys go in `X-Api-Key` or
  `Authorization: Bearer`, never in the address. Admins see and revoke everyone's keys in
  People & access. The guide is in [docs/api.md](https://github.com/tristenlammi/Arrmada/blob/main/docs/api.md).
- **Display titles** (Settings → Library → Display titles): English, original title, or a
  language of your choice, plus original titles for chosen languages. A title can be set
  one at a time from its ⋯ menu. Matching, metadata and file names keep using the canonical
  title. API responses add `display_title` beside `title` (#27).
- **Command palette** (Ctrl K / ⌘K): titles, pages, Settings cards and a few safe actions.
  Requesters get their own pages and Discover (#17).
- **Book naming templates** in Settings → Naming & metadata: audiobook and ebook folder and
  file templates with a live preview, an Audiobookshelf preset, and "Preview book rename…"
  for books already in the library. Listening places and app downloads survive a rename.
  The default naming is exactly the old one (#25).
- **A downloads folder per Movies or TV folder** (Settings → Library). Torrents for that
  folder's titles save there and import as hardlinks (#11).
- **Scan one library folder.** Adding a folder that already holds something offers a scan.
  Each extra folder has its own Scan button and says how much isn't in the library yet (#3).
- **TV searches use translated titles** in the "Also match titles in" languages. A German
  tracker's "Haus des Geldes" is found as Money Heist, but only with evidence beyond the
  name (language, year or country).
- **Homepage dashboard widget** for gethomepage.dev: counts only, with its own key
  (Settings → System → Dashboard widget) (#18).
- **Media folder on the host through `.env`** (`ARRMADA_MEDIA_HOST`), with no override file
  needed (#19).
- **Library scans** get 12 hours instead of minutes, stop cleanly, and have a Stop button
  (#22).
- **GPU guide** ([docs/gpu.md](https://github.com/tristenlammi/Arrmada/blob/main/docs/gpu.md)) for Intel, AMD and NVIDIA. AMD cards now use
  VAAPI automatically (#20).

### Fixed

- Upgrades: the bitrate ceiling no longer stops the search for a file that's still missing
  a Must (for example an H.264 file under "Must HEVC").
- Book renames are journaled and recover after a crash. Audiobook file ids stay stable, so
  Lissen downloads keep working (#25).
- Create it makes the folder that's in use instead of answering "unchanged" (#24).
- The setup wizard no longer warns about managed storage when `/media` is a host folder
  (#19).
- A folder scan of a dead network share says "not responding" instead of hanging the page
  (#3).

## [0.3.0] - 2026-10-17

### Highlights

- **More than one Movies and TV folder**, such as Kids, Animation or Anime, with rules for
  where new titles go and a Move to folder job (#3).
- **Import from Radarr and Sonarr**, files and all, without re-downloading.
- **64-bit ARM** (Raspberry Pi 4/5, ARM NAS boxes) and an **Unraid all-in-one image** with
  qBittorrent and FlareSolverr built in.
- **Audio language rules** in quality profiles, with language badges on files.
- **n8n and webhook alerts** with signed JSON.
- **Release previews**: admins see which release a waiting request would grab.

### Before you update

- **Ordered ranking (v2) is now the default** for choosing and upgrading releases. Upgrades
  follow the same rules as grabs. If you preferred the old behaviour, pick Classic in the
  Ranking switch on the Quality page (Advanced). A saved Classic choice is kept.
- **The bundled qBittorrent (Compose installs) gets a password.** Its own page now asks for
  a login. Admins find it on the qBittorrent card in Settings → Connections (Show its
  login). Torrents and settings are kept
  ([qBittorrent now asks for a password](https://github.com/tristenlammi/Arrmada/blob/main/docs/updating.md#qbittorrent-now-asks-for-a-password)).
- **Indexers, Prowlarr and download clients now see the User-Agent
  `Arrmada/<version>`** instead of Go's default. Cloudflare-protected indexers that blocked
  the old one now work.
- **Rolling back to v0.2.0 needs `./update.sh --rollback --with-db`**, because of database
  migrations 0321 to 0328.

### New

- **Several Movies and TV folders**, each with an optional rule (animation, anime, kids),
  folder pickers when adding or approving, and Move to folder (#3).
- **Radarr / Sonarr import** in Settings → Import & migrate: preview, then import titles,
  profiles, monitoring and existing files where they are. Imported wanted titles are
  searched at a gentle pace.
- **arm64 images** alongside amd64. The installer accepts 64-bit ARM. On ARM, Convert and AI
  subtitles use the CPU.
- **Unraid all-in-one image** (`-aio` tags): Arrmada with qBittorrent and FlareSolverr in
  one container. On arm64 it has qBittorrent only.
- **Audio language** row in quality profiles (Must / Prefer / Avoid per language, or
  "Original language"), judged by the file's real tracks once downloaded. Language badges
  appear on movie files and episodes.
- **Titles in other languages** (Settings → Library): downloads and folders named in
  another language match their title.
- **n8n / Webhook alerts** in Settings → Alerts: signed, versioned JSON, with sample events
  for testing ([docs/n8n.md](https://github.com/tristenlammi/Arrmada/blob/main/docs/n8n.md)).
- **Release previews** for requests waiting for approval, and "Approve & grab this".
- **SABnzbd fetches the NZB itself** when an indexer blocks Arrmada's own download.
- An **Update** label beside the sidebar version when a new release is out.

### Changed

- Needs-you imports (Import anyway, Choose different…, Map files…, Import into…) run as
  jobs with copy progress, instead of timing out behind a proxy (#15).
- Files from a release group the profile avoids (YTS and the like) are replaced under
  every "Replace for" setting, not only "Any improvement".

### Fixed

- AI subtitles: whisper now runs on any x86-64 CPU. v0.2.0's crashed with "Illegal
  instruction" on CPUs without AVX2 (Celeron J4125/N5105, older i5/i7).
- A film released without a year in its name gets a real title, instead of being held as
  "doesn't match".
- An indexer that blocks Arrmada says so, instead of claiming the API key was refused.

## [0.2.0] - 2026-10-15

### Highlights

- **Jellyfin** alongside Plex: Watch buttons, library updates, Insights and Sign in with
  Jellyfin.
- **Usenet** through SABnzbd, optional and alongside qBittorrent.
- **Release channels**: installs follow published releases (`stable`) by default.

### Before you update

- **Updates now follow the stable channel.** `ARRMADA_CHANNEL=stable` (the default)
  downloads the published release. `main` compiles the latest code as before. An install
  without the line gets one on its next update and never moves backwards. An install
  running newer code than the release keeps its build
  ([Stable or main](https://github.com/tristenlammi/Arrmada/blob/main/docs/updating.md#stable-or-main)).
- **Plain `docker compose up -d` runs the published image** (`ARRMADA_APP_IMAGE`, default
  `ghcr.io/tristenlammi/arrmada:stable`) instead of compiling. Existing installs are pinned
  to their own build, so nothing switches silently.
- **Settings → Plex became Settings → Media servers**, covering both servers. Old links
  redirect.
- **Rolling back to v0.1.0 needs `./update.sh --rollback --with-db`**, because of database
  migrations 0310 to 0320.

### New

- **Jellyfin**: connection, library updates with path mappings, watch history in Insights
  (with a server filter), Watch links per server, and Sign in with Jellyfin (password or
  Quick Connect) with account linking.
- **Usenet (SABnzbd)**: add your own SABnzbd in Settings → Connections, or
  `install.sh --with-sabnzbd` for a bundled one. Newznab indexers are searched once a
  usenet client exists, with a Usenet-or-torrents preference. Failed and stalled jobs are
  handled, and `.torrent` and `.nzb` files can be uploaded.
- A version badge at the foot of the staff sidebar.

### Changed

- The image builds in about 2 GB of memory (it needed about 7 GB) and explains when it
  runs out.
- The example compose file works as a Portainer stack.

## [0.1.0] - 2026-10-14

The first tagged release (early development).

### Highlights

- **One app for movies, TV, books and requests.** Movies and TV (anime numbering
  included), ebooks and audiobooks, and music as a preview that's off by default. Searching,
  downloading, importing, renaming and upgrading, driven by quality profiles that describe
  the file you want.
- **Requests**: Discover, Overseerr-style requests with auto-approval, and Overseerr import.
- **Audiobook server** for Lissen and other Audiobookshelf apps, OPDS for e-readers, and
  Send to Kindle.
- **Plex**: library updates, Watch buttons, Plex sign-in, and Insights (Tautulli-style
  watch history and stats).
- **Media tools**: subtitles (embedded, OpenSubtitles or local Whisper AI) and Convert
  (HEVC/AV1 re-encodes checked against the original).
- **Bundled qBittorrent and FlareSolverr**, Torznab indexers and Prowlarr sync.
- **Safety nets**: hardlinked imports, a recycle bin, nightly backups, a blocklist, and
  `update.sh` with automatic rollback.

[0.3.2]: https://github.com/tristenlammi/Arrmada/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/tristenlammi/Arrmada/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/tristenlammi/Arrmada/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/tristenlammi/Arrmada/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/tristenlammi/Arrmada/releases/tag/v0.1.0
