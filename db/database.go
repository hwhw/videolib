package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"videolib/models"

	_ "github.com/mattn/go-sqlite3"
)

type Database struct {
	db *sql.DB
}

func Open(path string) (*Database, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	d := &Database{db: db}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration: %w", err)
	}

	return d, nil
}

func (d *Database) Close() error {
	return d.db.Close()
}

func (d *Database) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS videos (
			hash TEXT PRIMARY KEY,
			path TEXT NOT NULL UNIQUE,
			filename TEXT NOT NULL,
			directory TEXT NOT NULL,
			size INTEGER NOT NULL DEFAULT 0,
			duration REAL NOT NULL DEFAULT 0,
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			thumb_count INTEGER NOT NULL DEFAULT 0,
			main_thumb INTEGER NOT NULL DEFAULT -1,
			added_at TEXT NOT NULL,
			modified_at TEXT NOT NULL,
			file_mod_time TEXT NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS tags (
			hash TEXT NOT NULL,
			tag TEXT NOT NULL,
			PRIMARY KEY (hash, tag),
			FOREIGN KEY (hash) REFERENCES videos(hash) ON DELETE CASCADE
		)`,

		`CREATE INDEX IF NOT EXISTS idx_tags_tag ON tags(tag)`,
		`CREATE INDEX IF NOT EXISTS idx_tags_hash ON tags(hash)`,
		`CREATE INDEX IF NOT EXISTS idx_videos_path ON videos(path)`,
		`CREATE INDEX IF NOT EXISTS idx_videos_directory ON videos(directory)`,

		`CREATE VIRTUAL TABLE IF NOT EXISTS videos_fts USING fts5(
			hash UNINDEXED,
			filename,
			path,
			directory,
			content='videos',
			content_rowid='rowid',
			tokenize='unicode61 remove_diacritics 2'
		)`,

		`CREATE TRIGGER IF NOT EXISTS videos_ai AFTER INSERT ON videos BEGIN
			INSERT INTO videos_fts(rowid, hash, filename, path, directory)
			VALUES (new.rowid, new.hash, new.filename, new.path, new.directory);
		END`,

		`CREATE TRIGGER IF NOT EXISTS videos_ad AFTER DELETE ON videos BEGIN
			INSERT INTO videos_fts(videos_fts, rowid, hash, filename, path, directory)
			VALUES ('delete', old.rowid, old.hash, old.filename, old.path, old.directory);
		END`,

		`CREATE TRIGGER IF NOT EXISTS videos_au AFTER UPDATE ON videos BEGIN
			INSERT INTO videos_fts(videos_fts, rowid, hash, filename, path, directory)
			VALUES ('delete', old.rowid, old.hash, old.filename, old.path, old.directory);
			INSERT INTO videos_fts(rowid, hash, filename, path, directory)
			VALUES (new.rowid, new.hash, new.filename, new.path, new.directory);
		END`,
	}

	for _, m := range migrations {
		if _, err := d.db.Exec(m); err != nil {
			return fmt.Errorf("executing migration: %w\nSQL: %s", err, m)
		}
	}

	return nil
}

// === Video CRUD ===

func (d *Database) PutVideo(v *models.Video) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO videos (hash, path, filename, directory, size, duration, width, height,
			thumb_count, main_thumb, added_at, modified_at, file_mod_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(hash) DO UPDATE SET
			path=excluded.path, filename=excluded.filename, directory=excluded.directory,
			size=excluded.size, duration=excluded.duration, width=excluded.width, height=excluded.height,
			thumb_count=excluded.thumb_count, main_thumb=excluded.main_thumb,
			modified_at=excluded.modified_at, file_mod_time=excluded.file_mod_time
	`,
		v.Hash, v.Path, v.Filename, v.Directory, v.Size, v.Duration, v.Width, v.Height,
		v.ThumbCount, v.MainThumb,
		v.AddedAt.Format(time.RFC3339), v.ModifiedAt.Format(time.RFC3339),
		v.FileModTime.Format(time.RFC3339),
	)
	if err != nil {
		return err
	}

	// Replace tags
	_, err = tx.Exec("DELETE FROM tags WHERE hash = ?", v.Hash)
	if err != nil {
		return err
	}

	if len(v.Tags) > 0 {
		stmt, err := tx.Prepare("INSERT OR IGNORE INTO tags (hash, tag) VALUES (?, ?)")
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, tag := range v.Tags {
			tag = strings.ToLower(strings.TrimSpace(tag))
			if tag != "" {
				if _, err := stmt.Exec(v.Hash, tag); err != nil {
					return err
				}
			}
		}
	}

	return tx.Commit()
}

// UpdateVideoPath changes path/filename/directory for an existing video.
// Preserves tags, thumbnails, main_thumb, added_at.
func (d *Database) UpdateVideoPath(v *models.Video, oldPath string) error {
	_, err := d.db.Exec(`
		UPDATE videos SET
			path = ?, filename = ?, directory = ?,
			size = ?, file_mod_time = ?, modified_at = ?
		WHERE hash = ?
	`,
		v.Path, v.Filename, v.Directory,
		v.Size, v.FileModTime.Format(time.RFC3339), v.ModifiedAt.Format(time.RFC3339),
		v.Hash,
	)
	return err
}

func (d *Database) GetVideo(hash string) (*models.Video, error) {
	v := &models.Video{}
	var addedAt, modifiedAt, fileModTime string

	err := d.db.QueryRow(`
		SELECT hash, path, filename, directory, size, duration, width, height,
			thumb_count, main_thumb, added_at, modified_at, file_mod_time
		FROM videos WHERE hash = ?
	`, hash).Scan(
		&v.Hash, &v.Path, &v.Filename, &v.Directory, &v.Size, &v.Duration,
		&v.Width, &v.Height, &v.ThumbCount, &v.MainThumb,
		&addedAt, &modifiedAt, &fileModTime,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("video not found: %s", hash)
	}
	if err != nil {
		return nil, err
	}

	v.AddedAt, _ = time.Parse(time.RFC3339, addedAt)
	v.ModifiedAt, _ = time.Parse(time.RFC3339, modifiedAt)
	v.FileModTime, _ = time.Parse(time.RFC3339, fileModTime)

	tags, err := d.getVideoTags(v.Hash)
	if err != nil {
		return nil, err
	}
	v.Tags = tags

	return v, nil
}

func (d *Database) DeleteVideo(hash string) error {
	_, err := d.db.Exec("DELETE FROM videos WHERE hash = ?", hash)
	return err
}

func (d *Database) GetAllPaths() (map[string]string, error) {
	rows, err := d.db.Query("SELECT path, hash FROM videos")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	paths := make(map[string]string)
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		paths[path] = hash
	}
	return paths, rows.Err()
}

func (d *Database) GetAllHashes() (map[string]bool, error) {
	rows, err := d.db.Query("SELECT hash FROM videos")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hashes := make(map[string]bool)
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		hashes[hash] = true
	}
	return hashes, rows.Err()
}

// === Full-Text Search ===

func (d *Database) FullTextSearch(query string) ([]*models.Video, error) {
	ftsQuery := sanitizeFTSQuery(query)

	rows, err := d.db.Query(`
		SELECT v.hash, v.path, v.filename, v.directory, v.size, v.duration,
			v.width, v.height, v.thumb_count, v.main_thumb,
			v.added_at, v.modified_at, v.file_mod_time
		FROM videos_fts f
		JOIN videos v ON v.hash = f.hash
		WHERE videos_fts MATCH ?
		ORDER BY rank
	`, ftsQuery)
	if err != nil {
		return nil, fmt.Errorf("FTS query error: %w (query: %s)", err, ftsQuery)
	}
	defer rows.Close()

	return d.scanVideosWithTags(rows)
}

func sanitizeFTSQuery(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	var parts []string
	inQuote := false
	var current strings.Builder

	for i := 0; i < len(input); i++ {
		ch := input[i]
		switch {
		case ch == '"':
			if inQuote {
				current.WriteByte('"')
				parts = append(parts, current.String())
				current.Reset()
				inQuote = false
			} else {
				if current.Len() > 0 {
					parts = append(parts, current.String())
					current.Reset()
				}
				current.WriteByte('"')
				inQuote = true
			}
		case ch == ' ' && !inQuote:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		case ch == '*':
			current.WriteByte('*')
		default:
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
				(ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.' {
				current.WriteByte(ch)
			} else if inQuote && ch == ' ' {
				// Allow spaces inside quotes for FTS phrase
				current.WriteByte(' ')
			}
		}
	}

	if current.Len() > 0 {
		s := current.String()
		if inQuote {
			s += `"`
		}
		parts = append(parts, s)
	}

	return strings.Join(parts, " AND ")
}

// === Tag Query Engine ===

// SearchByQuery parses and executes a structured search query.
// Syntax:
//   word              - FTS on filename/path (supports word* prefix)
//   "exact phrase"    - FTS phrase search
//   tag:value         - videos with tag exactly matching "value"
//   tag:val*          - videos with tag matching "val%" (wildcard)
//   duration:+1:30    - videos >= 90 seconds
//   duration:-60      - videos < 60 seconds
//   size:+100m        - videos >= 100 MiB
//   size:-1g          - videos < 1 GiB
//   UNTAGGED          - videos with no tags
//   TAGGED            - videos with at least one tag
//   AND, OR, NOT      - boolean operators (AND is implicit between terms)
//   ( )               - grouping
func (d *Database) SearchByQuery(query string) ([]*models.Video, error) {
	tokens := tokenizeQuery(query)
	if len(tokens) == 0 {
		return d.ListAllVideos()
	}

	parser := &queryParser{tokens: tokens, pos: 0}
	sqlWhere, args, err := parser.parseExpression()
	if err != nil {
		return nil, fmt.Errorf("query parse error: %w", err)
	}

	sqlStr := `
		SELECT DISTINCT v.hash, v.path, v.filename, v.directory, v.size, v.duration,
			v.width, v.height, v.thumb_count, v.main_thumb,
			v.added_at, v.modified_at, v.file_mod_time
		FROM videos v
		WHERE ` + sqlWhere + `
		ORDER BY v.filename
	`

	rows, err := d.db.Query(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("search query error: %w\nSQL: %s\nArgs: %v", err, sqlStr, args)
	}
	defer rows.Close()

	return d.scanVideosWithTags(rows)
}

type tokenType int

const (
	tokWord     tokenType = iota
	tokPhrase             // "quoted phrase"
	tokTag                // tag:value
	tokDuration           // duration:+90 or duration:-1:30:00
	tokSize               // size:+100m or size:-1g
	tokPath               // path:some/glob*pattern
	tokIPath              // ipath:some/glob*pattern (case-insensitive)
	tokAnd
	tokOr
	tokNot
	tokLParen
	tokRParen
)

type token struct {
	typ tokenType
	val string
}

func tokenizeQuery(input string) []token {
	var tokens []token
	input = strings.TrimSpace(input)
	i := 0

	for i < len(input) {
		// Skip whitespace
		if input[i] == ' ' || input[i] == '\t' {
			i++
			continue
		}

		// Parentheses
		if input[i] == '(' {
			tokens = append(tokens, token{tokLParen, "("})
			i++
			continue
		}
		if input[i] == ')' {
			tokens = append(tokens, token{tokRParen, ")"})
			i++
			continue
		}

		// Quoted phrase (standalone, not after a prefix)
		if input[i] == '"' {
			i++ // skip opening quote
			start := i
			for i < len(input) && input[i] != '"' {
				i++
			}
			phrase := input[start:i]
			if i < len(input) {
				i++ // skip closing quote
			}
			phrase = strings.TrimSpace(phrase)
			if phrase != "" {
				tokens = append(tokens, token{tokPhrase, phrase})
			}
			continue
		}

		// Read a word (until space, paren, or standalone quote)
		start := i
		for i < len(input) && input[i] != ' ' && input[i] != '\t' &&
			input[i] != '(' && input[i] != ')' {
			// If we hit a quote, it might be part of a prefix value like path:"foo bar"
			if input[i] == '"' {
				break
			}
			i++
		}

		word := input[start:i]
		lower := strings.ToLower(word)

		// Check if this word is a prefix that expects a value which might be quoted
		// Prefixes: tag:, duration:, size:, path:, ipath:
		prefixType, prefixLen := classifyPrefix(lower)
		if prefixType != tokWord && prefixLen == len(word) {
			// The prefix ends right at the word boundary — value follows (possibly quoted)
			val := readPrefixValue(input, &i)
			if val != "" {
				switch prefixType {
				case tokTag:
					tokens = append(tokens, token{tokTag, strings.ToLower(val)})
				case tokDuration:
					tokens = append(tokens, token{tokDuration, strings.ToLower(val)})
				case tokSize:
					tokens = append(tokens, token{tokSize, strings.ToLower(val)})
				case tokPath:
					tokens = append(tokens, token{tokPath, val})
				case tokIPath:
					tokens = append(tokens, token{tokIPath, val})
				}
			}
			continue
		}

		// Prefix with inline value like tag:foo, duration:+60, path:some/dir/*
		if prefixType != tokWord && prefixLen < len(word) {
			val := word[prefixLen:]
			switch prefixType {
			case tokTag:
				tokens = append(tokens, token{tokTag, strings.ToLower(val)})
			case tokDuration:
				tokens = append(tokens, token{tokDuration, strings.ToLower(val)})
			case tokSize:
				tokens = append(tokens, token{tokSize, strings.ToLower(val)})
			case tokPath:
				tokens = append(tokens, token{tokPath, val})
			case tokIPath:
				tokens = append(tokens, token{tokIPath, val})
			}
			continue
		}

		// Keywords
		upper := strings.ToUpper(word)
		switch upper {
		case "AND":
			tokens = append(tokens, token{tokAnd, "AND"})
		case "OR":
			tokens = append(tokens, token{tokOr, "OR"})
		case "NOT":
			tokens = append(tokens, token{tokNot, "NOT"})
		default:
			tokens = append(tokens, token{tokWord, word})
		}
	}

	return tokens
}

// classifyPrefix checks if a lowercase word starts with a known prefix.
// Returns the token type and the length of the prefix (including the colon).
func classifyPrefix(lower string) (tokenType, int) {
	prefixes := []struct {
		prefix string
		typ    tokenType
	}{
		{"ipath:", tokIPath}, // check before "path:" since it's longer
		{"path:", tokPath},
		{"tag:", tokTag},
		{"duration:", tokDuration},
		{"size:", tokSize},
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p.prefix) {
			return p.typ, len(p.prefix)
		}
	}
	return tokWord, 0
}

// readPrefixValue reads the value after a prefix like path: which may be quoted.
// Advances i past the value.
func readPrefixValue(input string, i *int) string {
	if *i >= len(input) {
		return ""
	}

	// Quoted value
	if input[*i] == '"' {
		*i++ // skip opening quote
		start := *i
		for *i < len(input) && input[*i] != '"' {
			*i++
		}
		val := input[start:*i]
		if *i < len(input) {
			*i++ // skip closing quote
		}
		return val
	}

	// Unquoted: read until space or paren
	start := *i
	for *i < len(input) && input[*i] != ' ' && input[*i] != '\t' &&
		input[*i] != '(' && input[*i] != ')' {
		*i++
	}
	return input[start:*i]
}

type queryParser struct {
	tokens []token
	pos    int
}

func (p *queryParser) peek() *token {
	if p.pos >= len(p.tokens) {
		return nil
	}
	return &p.tokens[p.pos]
}

func (p *queryParser) next() *token {
	if p.pos >= len(p.tokens) {
		return nil
	}
	t := &p.tokens[p.pos]
	p.pos++
	return t
}

func (p *queryParser) parseExpression() (string, []interface{}, error) {
	left, args, err := p.parseAnd()
	if err != nil {
		return "", nil, err
	}

	for {
		t := p.peek()
		if t == nil || t.typ != tokOr {
			break
		}
		p.next()

		right, rightArgs, err := p.parseAnd()
		if err != nil {
			return "", nil, err
		}

		left = "(" + left + " OR " + right + ")"
		args = append(args, rightArgs...)
	}

	return left, args, nil
}

func (p *queryParser) parseAnd() (string, []interface{}, error) {
	left, args, err := p.parseUnary()
	if err != nil {
		return "", nil, err
	}

	for {
		t := p.peek()
		if t == nil {
			break
		}

		if t.typ == tokAnd {
			p.next()
		} else if t.typ == tokWord || t.typ == tokPhrase || t.typ == tokTag ||
			t.typ == tokDuration || t.typ == tokSize ||
			t.typ == tokPath || t.typ == tokIPath ||
			t.typ == tokNot || t.typ == tokLParen {
			// implicit AND
		} else {
			break
		}

		right, rightArgs, err := p.parseUnary()
		if err != nil {
			return "", nil, err
		}

		left = "(" + left + " AND " + right + ")"
		args = append(args, rightArgs...)
	}

	return left, args, nil
}

func (p *queryParser) parseUnary() (string, []interface{}, error) {
	t := p.peek()
	if t == nil {
		return "", nil, fmt.Errorf("unexpected end of query")
	}

	if t.typ == tokNot {
		p.next()
		inner, args, err := p.parseUnary()
		if err != nil {
			return "", nil, err
		}
		return "NOT (" + inner + ")", args, nil
	}

	return p.parseAtom()
}

func (p *queryParser) parseAtom() (string, []interface{}, error) {
	t := p.peek()
	if t == nil {
		return "", nil, fmt.Errorf("unexpected end of query")
	}

	// Parenthesized sub-expression
	if t.typ == tokLParen {
		p.next()
		expr, args, err := p.parseExpression()
		if err != nil {
			return "", nil, err
		}
		closing := p.next()
		if closing == nil || closing.typ != tokRParen {
			return "", nil, fmt.Errorf("expected closing parenthesis")
		}
		return expr, args, nil
	}

	// Tag search
	if t.typ == tokTag {
		p.next()
		tagValue := t.val
		if strings.Contains(tagValue, "*") {
			likePattern := strings.ReplaceAll(tagValue, "*", "%")
			return "v.hash IN (SELECT hash FROM tags WHERE tag LIKE ?)", []interface{}{likePattern}, nil
		}
		return "v.hash IN (SELECT hash FROM tags WHERE tag = ?)", []interface{}{tagValue}, nil
	}

	// Duration filter
	if t.typ == tokDuration {
		p.next()
		return parseDurationFilter(t.val)
	}

	// Size filter
	if t.typ == tokSize {
		p.next()
		return parseSizeFilter(t.val)
	}

	// Path glob (case-sensitive)
	if t.typ == tokPath {
		p.next()
		return parsePathFilter(t.val, false)
	}

	// Path glob (case-insensitive)
	if t.typ == tokIPath {
		p.next()
		return parsePathFilter(t.val, true)
	}

	// Quoted phrase -> FTS phrase search
	if t.typ == tokPhrase {
		p.next()
		ftsPhrase := sanitizeFTSPhrase(t.val)
		if ftsPhrase == "" {
			return "1=1", nil, nil
		}
		return `v.hash IN (SELECT hash FROM videos_fts WHERE videos_fts MATCH ?)`,
			[]interface{}{ftsPhrase}, nil
	}

	// Plain word
	if t.typ != tokWord {
		return "", nil, fmt.Errorf("unexpected token: %s", t.val)
	}
	p.next()

	word := t.val
	upper := strings.ToUpper(word)

	if upper == "UNTAGGED" {
		return "v.hash NOT IN (SELECT DISTINCT hash FROM tags)", nil, nil
	}

	if upper == "TAGGED" {
		return "v.hash IN (SELECT DISTINCT hash FROM tags)", nil, nil
	}

	ftsWord := sanitizeFTSWord(word)
	if ftsWord == "" {
		return "1=1", nil, nil
	}

	return `v.hash IN (SELECT hash FROM videos_fts WHERE videos_fts MATCH ?)`,
		[]interface{}{ftsWord}, nil
}

// sanitizeFTSWord cleans a single word for FTS5 query
func sanitizeFTSWord(word string) string {
	var b strings.Builder
	for _, ch := range word {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.' {
			b.WriteRune(ch)
		} else if ch == '*' {
			b.WriteRune('*')
		}
	}
	return b.String()
}

// sanitizeFTSPhrase cleans a quoted phrase for FTS5 phrase query
func sanitizeFTSPhrase(phrase string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, ch := range phrase {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.' || ch == ' ' {
			b.WriteRune(ch)
		}
	}
	b.WriteByte('"')
	result := b.String()
	if result == `""` {
		return ""
	}
	return result
}

// parseDurationFilter parses duration:+VALUE or duration:-VALUE
// VALUE can be: SS, MM:SS, HH:MM:SS
func parseDurationFilter(val string) (string, []interface{}, error) {
	if len(val) < 2 {
		return "", nil, fmt.Errorf("invalid duration filter: %s (need + or - prefix)", val)
	}

	op := val[0]
	if op != '+' && op != '-' {
		return "", nil, fmt.Errorf("invalid duration filter: %s (need + or - prefix)", val)
	}

	seconds, err := parseDurationValue(val[1:])
	if err != nil {
		return "", nil, fmt.Errorf("invalid duration filter: %s: %w", val, err)
	}

	if op == '+' {
		return "v.duration >= ?", []interface{}{seconds}, nil
	}
	return "v.duration < ?", []interface{}{seconds}, nil
}

// parseDurationValue parses SS, MM:SS, or HH:MM:SS into total seconds
func parseDurationValue(s string) (float64, error) {
	parts := strings.Split(s, ":")

	switch len(parts) {
	case 1:
		// Just seconds
		return strconv.ParseFloat(parts[0], 64)

	case 2:
		// MM:SS
		mins, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid minutes: %s", parts[0])
		}
		secs, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid seconds: %s", parts[1])
		}
		return mins*60 + secs, nil

	case 3:
		// HH:MM:SS
		hours, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid hours: %s", parts[0])
		}
		mins, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid minutes: %s", parts[1])
		}
		secs, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid seconds: %s", parts[2])
		}
		return hours*3600 + mins*60 + secs, nil

	default:
		return 0, fmt.Errorf("invalid duration format: %s (use SS, MM:SS, or HH:MM:SS)", s)
	}
}

// parseSizeFilter parses size:+VALUE or size:-VALUE
// VALUE can be a number with optional suffix: k, m, g (binary: KiB, MiB, GiB)
func parseSizeFilter(val string) (string, []interface{}, error) {
	if len(val) < 2 {
		return "", nil, fmt.Errorf("invalid size filter: %s (need + or - prefix)", val)
	}

	op := val[0]
	if op != '+' && op != '-' {
		return "", nil, fmt.Errorf("invalid size filter: %s (need + or - prefix)", val)
	}

	bytes, err := parseSizeValue(val[1:])
	if err != nil {
		return "", nil, fmt.Errorf("invalid size filter: %s: %w", val, err)
	}

	if op == '+' {
		return "v.size >= ?", []interface{}{bytes}, nil
	}
	return "v.size < ?", []interface{}{bytes}, nil
}

// parseSizeValue parses a number with optional k/m/g suffix
func parseSizeValue(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size value")
	}

	var multiplier int64 = 1
	last := strings.ToLower(s[len(s)-1:])

	switch last {
	case "k":
		multiplier = 1024
		s = s[:len(s)-1]
	case "m":
		multiplier = 1024 * 1024
		s = s[:len(s)-1]
	case "g":
		multiplier = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	}

	num, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %s", s)
	}

	return int64(num * float64(multiplier)), nil
}

// parsePathFilter converts a shell glob pattern to a SQL GLOB or LIKE query on v.path.
// Shell glob rules: * matches any chars, ? matches single char, [abc] matches character class.
// SQLite GLOB uses the same rules natively, so we pass through directly.
// For case-insensitive mode, we convert to LIKE with % and _ and lowercase both sides.
func parsePathFilter(pattern string, caseInsensitive bool) (string, []interface{}, error) {
	if pattern == "" {
		return "1=1", nil, nil
	}

	if caseInsensitive {
		// Convert glob to LIKE pattern:
		// * -> %
		// ? -> _
		// Character classes [abc] are not supported in LIKE, translate to %
		likePattern := globToLike(pattern)
		return "LOWER(v.path) LIKE LOWER(?)", []interface{}{likePattern}, nil
	}

	// Case-sensitive: use SQLite GLOB directly
	// SQLite GLOB is case-sensitive and uses * and ? like shell
	return "v.path GLOB ?", []interface{}{pattern}, nil
}

// globToLike converts shell glob syntax to SQL LIKE syntax.
// * -> %, ? -> _, [...]  -> % (simplified), literal % and _ are escaped.
func globToLike(glob string) string {
	var b strings.Builder
	i := 0
	for i < len(glob) {
		ch := glob[i]
		switch ch {
		case '*':
			b.WriteByte('%')
		case '?':
			b.WriteByte('_')
		case '[':
			// Skip to closing ] and replace with %
			j := i + 1
			for j < len(glob) && glob[j] != ']' {
				j++
			}
			if j < len(glob) {
				i = j // will be incremented below
			}
			b.WriteByte('%')
		case '%':
			b.WriteString(`\%`)
		case '_':
			b.WriteString(`\_`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteByte(ch)
		}
		i++
	}
	return b.String()
}

// === Simple Tag Operations ===

func (d *Database) getVideoTags(hash string) ([]string, error) {
	rows, err := d.db.Query("SELECT tag FROM tags WHERE hash = ? ORDER BY tag", hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	if tags == nil {
		tags = []string{}
	}
	return tags, rows.Err()
}

func (d *Database) AddTags(hash string, tags []string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO tags (hash, tag) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		t = strings.ReplaceAll(t, " ", "")
		if t != "" {
			if _, err := stmt.Exec(hash, t); err != nil {
				return err
			}
		}
	}

	_, err = tx.Exec("UPDATE videos SET modified_at = ? WHERE hash = ?",
		time.Now().Format(time.RFC3339), hash)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (d *Database) SetTags(hash string, tags []string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM tags WHERE hash = ?", hash); err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO tags (hash, tag) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	seen := make(map[string]bool)
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		t = strings.ReplaceAll(t, " ", "")
		if t != "" && !seen[t] {
			seen[t] = true
			if _, err := stmt.Exec(hash, t); err != nil {
				return err
			}
		}
	}

	_, err = tx.Exec("UPDATE videos SET modified_at = ? WHERE hash = ?",
		time.Now().Format(time.RFC3339), hash)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (d *Database) RemoveTags(hash string, tags []string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("DELETE FROM tags WHERE hash = ? AND tag = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		t = strings.ReplaceAll(t, " ", "")
		if t != "" {
			stmt.Exec(hash, t)
		}
	}

	_, err = tx.Exec("UPDATE videos SET modified_at = ? WHERE hash = ?",
		time.Now().Format(time.RFC3339), hash)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (d *Database) BulkAddTags(hashes []string, tags []string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO tags (hash, tag) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	updateStmt, err := tx.Prepare("UPDATE videos SET modified_at = ? WHERE hash = ?")
	if err != nil {
		return err
	}
	defer updateStmt.Close()

	for _, hash := range hashes {
		for _, t := range tags {
			t = strings.ToLower(strings.TrimSpace(t))
			t = strings.ReplaceAll(t, " ", "")
			if t != "" {
				stmt.Exec(hash, t)
			}
		}
		updateStmt.Exec(now, hash)
	}

	return tx.Commit()
}

func (d *Database) BulkRemoveTags(hashes []string, tags []string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("DELETE FROM tags WHERE hash = ? AND tag = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	updateStmt, err := tx.Prepare("UPDATE videos SET modified_at = ? WHERE hash = ?")
	if err != nil {
		return err
	}
	defer updateStmt.Close()

	for _, hash := range hashes {
		for _, t := range tags {
			t = strings.ToLower(strings.TrimSpace(t))
			t = strings.ReplaceAll(t, " ", "")
			if t != "" {
				stmt.Exec(hash, t)
			}
		}
		updateStmt.Exec(now, hash)
	}

	return tx.Commit()
}

func (d *Database) SetMainThumb(hash string, thumbIndex int) error {
	_, err := d.db.Exec(
		"UPDATE videos SET main_thumb = ?, modified_at = ? WHERE hash = ?",
		thumbIndex, time.Now().Format(time.RFC3339), hash,
	)
	return err
}

// === Listing ===

func (d *Database) ListAllVideos() ([]*models.Video, error) {
	rows, err := d.db.Query(`
		SELECT hash, path, filename, directory, size, duration, width, height,
			thumb_count, main_thumb, added_at, modified_at, file_mod_time
		FROM videos ORDER BY filename
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return d.scanVideosWithTags(rows)
}

func (d *Database) ListAllTags() ([]models.TagInfo, error) {
	rows, err := d.db.Query(`
		SELECT tag, COUNT(*) as cnt
		FROM tags
		GROUP BY tag
		ORDER BY tag
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []models.TagInfo
	for rows.Next() {
		var t models.TagInfo
		if err := rows.Scan(&t.Name, &t.Count); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// === Export / Import ===

func (d *Database) Export() (*models.ExportData, error) {
	videos, err := d.ListAllVideos()
	if err != nil {
		return nil, err
	}
	return &models.ExportData{
		Version:  1,
		Exported: time.Now().UTC().Format(time.RFC3339),
		Videos:   videos,
	}, nil
}

func (d *Database) Import(data *models.ExportData) (added, updated, skipped int, err error) {
	for _, v := range data.Videos {
		existing, existErr := d.GetVideo(v.Hash)
		if existErr != nil {
			if putErr := d.PutVideo(v); putErr != nil {
				skipped++
				continue
			}
			added++
		} else {
			existingTags := make(map[string]bool)
			for _, t := range existing.Tags {
				existingTags[t] = true
			}

			var newTags []string
			for _, t := range v.Tags {
				t = strings.ToLower(strings.TrimSpace(t))
				if t != "" && !existingTags[t] {
					newTags = append(newTags, t)
				}
			}

			changed := false
			if len(newTags) > 0 {
				d.AddTags(existing.Hash, newTags)
				changed = true
			}
			if existing.MainThumb <= 0 && v.MainThumb > 0 {
				d.SetMainThumb(existing.Hash, v.MainThumb)
				changed = true
			}

			if changed {
				updated++
			} else {
				skipped++
			}
		}
	}
	return
}

// === Helpers ===

func (d *Database) scanVideosWithTags(rows *sql.Rows) ([]*models.Video, error) {
	var videos []*models.Video
	for rows.Next() {
		v := &models.Video{}
		var addedAt, modifiedAt, fileModTime string

		err := rows.Scan(
			&v.Hash, &v.Path, &v.Filename, &v.Directory, &v.Size, &v.Duration,
			&v.Width, &v.Height, &v.ThumbCount, &v.MainThumb,
			&addedAt, &modifiedAt, &fileModTime,
		)
		if err != nil {
			return nil, err
		}

		v.AddedAt, _ = time.Parse(time.RFC3339, addedAt)
		v.ModifiedAt, _ = time.Parse(time.RFC3339, modifiedAt)
		v.FileModTime, _ = time.Parse(time.RFC3339, fileModTime)

		tags, err := d.getVideoTags(v.Hash)
		if err != nil {
			return nil, err
		}
		v.Tags = tags

		videos = append(videos, v)
	}

	if videos == nil {
		videos = []*models.Video{}
	}

	return videos, rows.Err()
}

func (d *Database) SearchByTags(tags []string) ([]*models.Video, error) {
	if len(tags) == 0 {
		return d.ListAllVideos()
	}

	normalized := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			normalized = append(normalized, t)
		}
	}
	sort.Strings(normalized)

	var parts []string
	var args []interface{}
	for _, t := range normalized {
		parts = append(parts, "SELECT hash FROM tags WHERE tag = ?")
		args = append(args, t)
	}

	hashQuery := strings.Join(parts, " INTERSECT ")

	sqlStr := `
		SELECT v.hash, v.path, v.filename, v.directory, v.size, v.duration,
			v.width, v.height, v.thumb_count, v.main_thumb,
			v.added_at, v.modified_at, v.file_mod_time
		FROM videos v
		WHERE v.hash IN (` + hashQuery + `)
		ORDER BY v.filename
	`

	rows, err := d.db.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return d.scanVideosWithTags(rows)
}
