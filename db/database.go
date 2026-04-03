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
			title TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
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
			title,
			content='videos',
			content_rowid='rowid',
			tokenize='unicode61 remove_diacritics 2'
		)`,

		`CREATE TRIGGER IF NOT EXISTS videos_ai AFTER INSERT ON videos BEGIN
			INSERT INTO videos_fts(rowid, hash, filename, path, directory, title)
			VALUES (new.rowid, new.hash, new.filename, new.path, new.directory, new.title);
		END`,

		`CREATE TRIGGER IF NOT EXISTS videos_ad AFTER DELETE ON videos BEGIN
			INSERT INTO videos_fts(videos_fts, rowid, hash, filename, path, directory, title)
			VALUES ('delete', old.rowid, old.hash, old.filename, old.path, old.directory, old.title);
		END`,

		`CREATE TRIGGER IF NOT EXISTS videos_au AFTER UPDATE ON videos BEGIN
			INSERT INTO videos_fts(videos_fts, rowid, hash, filename, path, directory, title)
			VALUES ('delete', old.rowid, old.hash, old.filename, old.path, old.directory, old.title);
			INSERT INTO videos_fts(rowid, hash, filename, path, directory, title)
			VALUES (new.rowid, new.hash, new.filename, new.path, new.directory, new.title);
		END`,
	}

	for _, m := range migrations {
		if _, err := d.db.Exec(m); err != nil {
			return fmt.Errorf("executing migration: %w\nSQL: %s", err, m)
		}
	}

	// Add columns if upgrading from older schema
	d.addColumnIfMissing("videos", "title", "TEXT NOT NULL DEFAULT ''")
	d.addColumnIfMissing("videos", "description", "TEXT NOT NULL DEFAULT ''")

	return nil
}

func (d *Database) addColumnIfMissing(table, column, colDef string) {
	var count int
	err := d.db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?",
		table, column,
	).Scan(&count)
	if err != nil || count > 0 {
		return
	}
	d.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, colDef))
}

// === Video CRUD ===

const videoColumns = `hash, path, filename, directory, size, duration, width, height,
	thumb_count, main_thumb, title, description, added_at, modified_at, file_mod_time`

func (d *Database) PutVideo(v *models.Video) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO videos (hash, path, filename, directory, size, duration, width, height,
			thumb_count, main_thumb, title, description, added_at, modified_at, file_mod_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(hash) DO UPDATE SET
			path=excluded.path, filename=excluded.filename, directory=excluded.directory,
			size=excluded.size, duration=excluded.duration, width=excluded.width, height=excluded.height,
			thumb_count=excluded.thumb_count, main_thumb=excluded.main_thumb,
			title=excluded.title, description=excluded.description,
			modified_at=excluded.modified_at, file_mod_time=excluded.file_mod_time
	`,
		v.Hash, v.Path, v.Filename, v.Directory, v.Size, v.Duration, v.Width, v.Height,
		v.ThumbCount, v.MainThumb, v.Title, v.Description,
		v.AddedAt.Format(time.RFC3339), v.ModifiedAt.Format(time.RFC3339),
		v.FileModTime.Format(time.RFC3339),
	)
	if err != nil {
		return err
	}

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
			tag = strings.ReplaceAll(tag, " ", "")
			if tag != "" {
				if _, err := stmt.Exec(v.Hash, tag); err != nil {
					return err
				}
			}
		}
	}

	return tx.Commit()
}

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
		SELECT `+videoColumns+`
		FROM videos WHERE hash = ?
	`, hash).Scan(
		&v.Hash, &v.Path, &v.Filename, &v.Directory, &v.Size, &v.Duration,
		&v.Width, &v.Height, &v.ThumbCount, &v.MainThumb,
		&v.Title, &v.Description,
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

func (d *Database) SetTitle(hash string, title string) error {
	_, err := d.db.Exec(
		"UPDATE videos SET title = ?, modified_at = ? WHERE hash = ?",
		title, time.Now().Format(time.RFC3339), hash,
	)
	return err
}

func (d *Database) SetDescription(hash string, description string) error {
	_, err := d.db.Exec(
		"UPDATE videos SET description = ?, modified_at = ? WHERE hash = ?",
		description, time.Now().Format(time.RFC3339), hash,
	)
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
			v.title, v.description,
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
			v.title, v.description,
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
	tokPhrase
	tokTag
	tokDuration
	tokSize
	tokPath
	tokIPath
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
		if input[i] == ' ' || input[i] == '\t' {
			i++
			continue
		}
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
		if input[i] == '"' {
			i++
			start := i
			for i < len(input) && input[i] != '"' {
				i++
			}
			phrase := input[start:i]
			if i < len(input) {
				i++
			}
			phrase = strings.TrimSpace(phrase)
			if phrase != "" {
				tokens = append(tokens, token{tokPhrase, phrase})
			}
			continue
		}

		start := i
		for i < len(input) && input[i] != ' ' && input[i] != '\t' &&
			input[i] != '(' && input[i] != ')' {
			if input[i] == '"' {
				break
			}
			i++
		}

		word := input[start:i]
		lower := strings.ToLower(word)

		prefixType, prefixLen := classifyPrefix(lower)
		if prefixType != tokWord && prefixLen == len(word) {
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

func classifyPrefix(lower string) (tokenType, int) {
	prefixes := []struct {
		prefix string
		typ    tokenType
	}{
		{"ipath:", tokIPath},
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

func readPrefixValue(input string, i *int) string {
	if *i >= len(input) {
		return ""
	}
	if input[*i] == '"' {
		*i++
		start := *i
		for *i < len(input) && input[*i] != '"' {
			*i++
		}
		val := input[start:*i]
		if *i < len(input) {
			*i++
		}
		return val
	}
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
	if t.typ == tokTag {
		p.next()
		tagValue := t.val
		if strings.Contains(tagValue, "*") {
			likePattern := strings.ReplaceAll(tagValue, "*", "%")
			return "v.hash IN (SELECT hash FROM tags WHERE tag LIKE ?)", []interface{}{likePattern}, nil
		}
		return "v.hash IN (SELECT hash FROM tags WHERE tag = ?)", []interface{}{tagValue}, nil
	}
	if t.typ == tokDuration {
		p.next()
		return parseDurationFilter(t.val)
	}
	if t.typ == tokSize {
		p.next()
		return parseSizeFilter(t.val)
	}
	if t.typ == tokPath {
		p.next()
		return parsePathFilter(t.val, false)
	}
	if t.typ == tokIPath {
		p.next()
		return parsePathFilter(t.val, true)
	}
	if t.typ == tokPhrase {
		p.next()
		ftsPhrase := sanitizeFTSPhrase(t.val)
		if ftsPhrase == "" {
			return "1=1", nil, nil
		}
		return `v.hash IN (SELECT hash FROM videos_fts WHERE videos_fts MATCH ?)`,
			[]interface{}{ftsPhrase}, nil
	}
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

func parseDurationFilter(val string) (string, []interface{}, error) {
	if len(val) < 2 {
		return "", nil, fmt.Errorf("invalid duration filter: %s", val)
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

func parseDurationValue(s string) (float64, error) {
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		return strconv.ParseFloat(parts[0], 64)
	case 2:
		mins, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, err
		}
		secs, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, err
		}
		return mins*60 + secs, nil
	case 3:
		hours, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, err
		}
		mins, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, err
		}
		secs, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return 0, err
		}
		return hours*3600 + mins*60 + secs, nil
	default:
		return 0, fmt.Errorf("invalid duration format: %s", s)
	}
}

func parseSizeFilter(val string) (string, []interface{}, error) {
	if len(val) < 2 {
		return "", nil, fmt.Errorf("invalid size filter: %s", val)
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

func parsePathFilter(pattern string, caseInsensitive bool) (string, []interface{}, error) {
	if pattern == "" {
		return "1=1", nil, nil
	}
	if caseInsensitive {
		likePattern := globToLike(pattern)
		return "LOWER(v.path) LIKE LOWER(?)", []interface{}{likePattern}, nil
	}
	return "v.path GLOB ?", []interface{}{pattern}, nil
}

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
			j := i + 1
			for j < len(glob) && glob[j] != ']' {
				j++
			}
			if j < len(glob) {
				i = j
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
			stmt.Exec(hash, t)
		}
	}
	tx.Exec("UPDATE videos SET modified_at = ? WHERE hash = ?", time.Now().Format(time.RFC3339), hash)
	return tx.Commit()
}

func (d *Database) SetTags(hash string, tags []string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.Exec("DELETE FROM tags WHERE hash = ?", hash)
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
			stmt.Exec(hash, t)
		}
	}
	tx.Exec("UPDATE videos SET modified_at = ? WHERE hash = ?", time.Now().Format(time.RFC3339), hash)
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
	tx.Exec("UPDATE videos SET modified_at = ? WHERE hash = ?", time.Now().Format(time.RFC3339), hash)
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
	upd, _ := tx.Prepare("UPDATE videos SET modified_at = ? WHERE hash = ?")
	defer upd.Close()
	for _, hash := range hashes {
		for _, t := range tags {
			t = strings.ToLower(strings.TrimSpace(t))
			t = strings.ReplaceAll(t, " ", "")
			if t != "" {
				stmt.Exec(hash, t)
			}
		}
		upd.Exec(now, hash)
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
	upd, _ := tx.Prepare("UPDATE videos SET modified_at = ? WHERE hash = ?")
	defer upd.Close()
	for _, hash := range hashes {
		for _, t := range tags {
			t = strings.ToLower(strings.TrimSpace(t))
			t = strings.ReplaceAll(t, " ", "")
			if t != "" {
				stmt.Exec(hash, t)
			}
		}
		upd.Exec(now, hash)
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
		SELECT ` + videoColumns + `
		FROM videos ORDER BY filename
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return d.scanVideosWithTags(rows)
}

func (d *Database) ListAllTags() ([]models.TagInfo, error) {
	rows, err := d.db.Query(`SELECT tag, COUNT(*) as cnt FROM tags GROUP BY tag ORDER BY tag`)
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
			changed := false

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
			if len(newTags) > 0 {
				d.AddTags(existing.Hash, newTags)
				changed = true
			}
			if existing.MainThumb <= 0 && v.MainThumb > 0 {
				d.SetMainThumb(existing.Hash, v.MainThumb)
				changed = true
			}
			if existing.Title == "" && v.Title != "" {
				d.SetTitle(existing.Hash, v.Title)
				changed = true
			}
			if existing.Description == "" && v.Description != "" {
				d.SetDescription(existing.Hash, v.Description)
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
			&v.Title, &v.Description,
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
			v.title, v.description,
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
