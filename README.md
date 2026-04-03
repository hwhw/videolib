# videolib

A self-contained video library server with tagging, search, and thumbnail previews. Scans directories for video files, extracts metadata via ffprobe, generates thumbnail strips, and serves a responsive web UI for browsing, searching, and tagging your collection. All data is stored in a local SQLite database. The entire application — including HTML templates, CSS, and JavaScript — compiles into a single binary.

## WARNING: fully vibe-coded with Claude Opus 4.6

see the included chat log for how this came to be.

## Building

### Prerequisites

- **Go 1.22+**
- **ffmpeg/ffprobe** (for metadata extraction and thumbnail generation)
- **GCC** or compatible C compiler (required by the SQLite driver)

```bash
# Ubuntu/Debian
sudo apt install ffmpeg gcc

# macOS
brew install ffmpeg

# Arch
sudo pacman -S ffmpeg
```

### Compile

```bash
git clone https://github.com/hwhw/videolib.git
cd videolib
CGO_ENABLED=1 go build -tags fts5 -o videolib .
```

The resulting binary is fully self-contained — all static assets are embedded.

### Cross-compilation

Since the SQLite driver uses CGO, cross-compilation requires a C cross-compiler:

```bash
CC=x86_64-linux-musl-gcc GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -o videolib .
```

## Web Frontend

The web interface is a responsive single-page application that works on desktop and mobile browsers.

### Video Grid

The main page displays videos in a card grid that adapts to screen size. Each card shows:

- A thumbnail image (the selected main thumbnail or the first generated one)
- Video duration badge
- Filename and directory path
- Up to 5 tag pills

**Thumbnail preview**: Hovering over a card (desktop) or tapping it (mobile) cycles through all generated thumbnails for that video, giving a quick preview without opening the player. The cycle runs once through all thumbnails and stops. On mobile, a second tap navigates to the video page.

**Tag navigation**: Clicking any tag pill in the grid navigates to a search for that tag.

### Search

A single unified search bar supports a rich query language:

| Query | Meaning |
|-------|---------|
| `holiday` | Full-text search in filenames and paths |
| `holiday*` | Prefix/truncated search |
| `"beach party"` | Exact phrase match in filenames/paths |
| `tag:action` | Videos with the tag "action" |
| `tag:genre*` | Videos with tags matching "genre..." |
| `UNTAGGED` | Videos with no tags at all |
| `TAGGED` | Videos with at least one tag |
| `duration:+1:30` | Videos 90 seconds or longer |
| `duration:-60` | Videos shorter than 60 seconds |
| `duration:+1:00:00` | Videos 1 hour or longer |
| `size:+100m` | Videos 100 MiB or larger |
| `size:-1g` | Videos smaller than 1 GiB |
| `path:videos/2024/*` | Path matching shell glob (case-sensitive) |
| `path:"my videos/*.mp4"` | Quoted glob with spaces |
| `ipath:*.mkv` | Case-insensitive path glob |
| `A AND B` | Both conditions must match |
| `A OR B` | Either condition matches |
| `NOT A` | Exclude matching videos |
| `(A OR B) AND NOT C` | Grouping with parentheses |
| `holiday tag:vacation` | Implicit AND between terms |

Duration values accept `SS`, `MM:SS`, or `HH:MM:SS` formats. Size values accept a number with optional `k`, `m`, or `g` suffix (binary KiB/MiB/GiB).

The search bar features **tag autocomplete** — typing `tag:` followed by characters shows a dropdown of matching existing tags, navigable with arrow keys and Tab/Enter.

### Sorting and Pagination

Search results can be sorted by:

- **Name** — filename (default)
- **Path** — full relative path including filename
- **Hash** — content hash (quasi-random but stable ordering)
- **Added** — date the video was first scanned
- **Modified** — date of last metadata/tag change
- **Duration** — video length
- **Size** — file size

Each sort column toggles between ascending and descending. Results are paginated with configurable page sizes of 50, 100 (default), 200, or 500 items.

### Bulk Tagging

Selecting one or more videos (via checkboxes) reveals a bulk action bar:

1. Check individual videos, use **Select Page** to select all on the current page, or **Select All** for the entire result set
2. Type comma-separated tags in the bulk input field (with autocomplete)
3. Click **+ Add Tags** or **- Remove Tags** to apply

This enables fast workflows like: search for `UNTAGGED`, select all, add initial tags.

### Single Video View

Clicking a video opens the player page with:

- **HTML5 video player** with poster image (selected thumbnail shown before playback starts)
- **Download button** to open the video file in an external player or save it locally
- **Thumbnail picker** — opens a grid of all generated thumbnails; click one to set it as the main thumbnail used in the grid view and as the player poster
- **Tag editor** — view, add, and remove tags; each tag is a clickable link that searches for other videos with the same tag
- **Similar videos** — automatically shown based on shared tags, ranked by number of tags in common

### Tags Page

A dedicated page lists all tags with usage counts. Clicking a tag navigates to the search results for that tag.

## Command Line

```
videolib <command> [options] [arguments]
```

### `serve` — Start the web server

```
videolib serve [options]
```

Starts the HTTP server. Video file paths are read from the database, so no directory arguments are needed.

| Option | Default | Description |
|--------|---------|-------------|
| `-db` | `videolib.db` | Database file path |
| `-thumbs` | `thumbnails` | Thumbnail directory |
| `-addr` | `:8080` | Listen address |
| `-title` | `Video Library` | Web application title shown in the navbar and browser tab |

**Examples:**

```bash
# Start on default port
videolib serve

# Custom port and title
videolib serve -addr :9090 -title "My Movies"

# Use a specific database
videolib serve -db /data/videos.db -thumbs /data/thumbs
```

### `scan` — Add videos to the database

```
videolib scan [options] <path> [<path> ...]
```

Each path can be:

- **A directory** — scanned recursively for video files
- **A single video file** — added directly
- **A JSON file** — previously exported with `videolib list -format json`, imported with tag merging

When scanning encounters a file whose content hash already exists in the database (e.g., a renamed or moved file), the database entry is updated to the new path while **preserving all tags, thumbnails, and the selected main thumbnail**.

Scanning does **not** remove entries for files that no longer exist — use `videolib scrub` for that.

| Option | Default | Description |
|--------|---------|-------------|
| `-db` | `videolib.db` | Database file path |
| `-thumbs` | `thumbnails` | Thumbnail directory |
| `-ext` | *(built-in list)* | Override video extensions (comma-separated) |
| `-add-ext` | *(none)* | Add extra extensions to the defaults |

Default extensions: `.3gp`, `.avi`, `.divx`, `.flv`, `.m4v`, `.mkv`, `.mov`, `.mp4`, `.mpeg`, `.mpg`, `.ogv`, `.rm`, `.rmvb`, `.ts`, `.vob`, `.webm`, `.wmv`

**Examples:**

```bash
# Scan a directory
videolib scan ./videos

# Scan multiple directories
videolib scan ./movies ./series ./downloads

# Scan a single file
videolib scan ./new-video.mp4

# Import a previously exported JSON
videolib scan backup.json

# Mix directories, files, and imports
videolib scan ./new-videos clip.mp4 old-tags.json

# Add support for extra file types
videolib scan -add-ext mts,m2ts ./camera-footage

# Only scan specific formats
videolib scan -ext mp4,mkv ./videos
```

### `scrub` — Clean up stale entries

```
videolib scrub [options]
```

Performs two cleanup operations in order:

1. **Database scrub**: Removes entries for video files that no longer exist on disk
2. **Thumbnail scrub**: Removes thumbnail directories that have no matching database entry

| Option | Default | Description |
|--------|---------|-------------|
| `-db` | `videolib.db` | Database file path |
| `-thumbs` | `thumbnails` | Thumbnail directory |
| `-no-videos` | `false` | Skip removing stale database entries |
| `-no-thumbs` | `false` | Skip removing orphaned thumbnails |
| `-dry-run` | `false` | Show what would be done without making changes |

**Examples:**

```bash
# Full cleanup
videolib scrub

# Preview what would be removed
videolib scrub -dry-run

# Only clean database, keep orphaned thumbnails
videolib scrub -no-thumbs

# Only clean thumbnails, keep stale database entries
videolib scrub -no-videos
```

### `list` — Export video data

```
videolib list [options] [search query]
```

Outputs video data in two formats. The optional search query uses the same syntax as the web frontend.

| Option | Default | Description |
|--------|---------|-------------|
| `-db` | `videolib.db` | Database file path |
| `-format` | `text` | Output format: `json` or `text` |
| `-output` | `-` (stdout) | Output file path |

**Text format** outputs one line per video:

```
HASH<tab>TAGS<tab>PATH
```

Tags are comma-separated with no spaces. Videos with no tags show `-`.

**JSON format** outputs a full export that can be re-imported with `videolib scan`.

**Examples:**

```bash
# List all videos
videolib list

# Search and list
videolib list tag:action
videolib list UNTAGGED
videolib list "duration:+1:00:00 AND size:+1g"
videolib list 'ipath:*/comedy/*'

# Export to JSON (importable backup)
videolib list -format json -output backup.json

# Export search results
videolib list -format json -output action.json tag:action

# Count videos matching a query
videolib list tag:action | wc -l

# Get just the paths
videolib list | cut -f3

# Get just the hashes
videolib list UNTAGGED | cut -f1
```

### `tags` — Modify tags from the command line

```
videolib tags [options] [<hash> ...]
```

Adds or removes tags for videos identified by hash. Hash arguments can be prefixes (minimum 8 characters). If no hash arguments are given, hashes are read from stdin — compatible with the text output of `videolib list`.

| Option | Default | Description |
|--------|---------|-------------|
| `-db` | `videolib.db` | Database file path |
| `-add` | *(none)* | Tag to add (repeatable) |
| `-remove` | *(none)* | Tag to remove (repeatable) |

**Examples:**

```bash
# Tag a single video
videolib tags -add action -add favorite abc123de

# Remove a tag
videolib tags -remove boring abc123de

# Multiple operations on multiple videos
videolib tags -add reviewed -remove needs-review abc123de def456ab

# Tag all untagged videos
videolib list UNTAGGED | videolib tags -add needs-review

# Tag search results
videolib list 'ipath:*/comedy/*' | videolib tags -add genre:comedy

# Remove a tag from everything that has it
videolib list tag:old-name | videolib tags -remove old-name -add new-name

# Tag all short videos
videolib list 'duration:-60' | videolib tags -add short

# Tag large files
videolib list 'size:+2g' | videolib tags -add large-file
```

### `hash` — Compute file content hashes

```
videolib hash [<file> ...]
```

Computes the same content hash used internally to identify videos. If no filenames are given, reads filenames from stdin (one per line). Outputs one hash per line. Prints `NOTFOUND` for files that don't exist.

**Examples:**

```bash
# Hash a single file
videolib hash video.mp4

# Hash multiple files
videolib hash *.mp4 *.mkv

# Hash from a file list
find /videos -name '*.mp4' | videolib hash

# Check if files match known hashes
videolib hash video.mp4
# compare with:
videolib list -format text | grep -F "$(videolib hash video.mp4)"

# Hash all paths from the database (verify integrity)
videolib list | cut -f3 | videolib hash
```

## Typical Workflows

### Initial setup

```bash
# Scan your video directories
videolib scan ./videos ./more-videos

# Start the server
videolib serve -title "My Library"
# Open http://localhost:8080
```

### Ongoing maintenance

```bash
# Add newly downloaded videos
videolib scan ./downloads

# Clean up after deleting files
videolib scrub

# Backup your tags and metadata
videolib list -format json -output backup.json

# Restore on another machine
videolib scan backup.json
videolib scan /path/to/same/videos
```

### Batch tagging from the command line

```bash
# Tag everything in a directory
videolib list 'ipath:videos/action/*' | videolib tags -add genre:action

# Find and tag unreviewed content
videolib list 'UNTAGGED' | videolib tags -add needs-review

# Rename a tag across the whole library
videolib list 'tag:old-tag' | videolib tags -remove old-tag -add new-tag
```

## File Identification

Videos are identified by a SHA-256 hash of the first 10 MB of file content combined with the total file size. This means:

- **Renaming or moving** a file preserves its identity — tags and thumbnails follow automatically on the next scan
- **Duplicate files** (identical content) share the same hash — the database tracks the most recently scanned path
- Files that differ only after the first 10 MB but have different total sizes are distinguished correctly

## License

MIT
