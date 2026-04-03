// === State ===
var allVideos = [];
var selectedHashes = new Set();
var thumbCache = {};
var thumbIntervals = {};
var allTagNames = [];
var isReadOnly = false;

var currentPage = 1;
var pageSize = 100;
var sortField = 'filename';
var sortDir = 'asc';

// === API Helpers ===
function api(url, options) {
    options = options || {};
    var headers = {};
    if (options.body) {
        headers['Content-Type'] = 'application/json';
    }
    return fetch(url, Object.assign({ headers: headers }, options))
        .then(function(resp) {
            if (!resp.ok) throw new Error('API error: ' + resp.status);
            return resp.json();
        });
}

function loadAllTagNames() {
    return api('/api/tags').then(function(tags) {
        allTagNames = (tags || []).map(function(t) { return t.name; });
        return allTagNames;
    }).catch(function() {
        allTagNames = [];
        return [];
    });
}

function loadConfig() {
    return api('/api/config').then(function(cfg) {
        isReadOnly = cfg.read_only || false;
    }).catch(function() {
        isReadOnly = false;
    });
}

// === Read-Only Mode ===
function setupReadOnlyMode() {
    isReadOnly = document.body.dataset.readonly === 'true';

    if (isReadOnly) {
        // Hide edit controls on video page
        hideElement('titleEditor');
        hideElement('descriptionEditor');
        hideElement('tagInputRow');

        // Remove tag delete buttons
        var removes = document.querySelectorAll('.tag-remove');
        removes.forEach(function(el) { el.style.display = 'none'; });

        // Hide thumb picker button & bulk bar triggers
        var thumbBtn = document.querySelector('[onclick="toggleThumbPicker()"]');
        if (thumbBtn) thumbBtn.style.display = 'none';
    } else {
        // Add edit buttons for title and description
        var titleEl = document.getElementById('videoTitle');
        if (titleEl) {
            var editBtn = document.createElement('button');
            editBtn.className = 'btn btn-sm btn-secondary edit-inline-btn';
            editBtn.textContent = '✏️';
            editBtn.title = 'Edit title';
            editBtn.addEventListener('click', function() { showTitleEditor(); });
            titleEl.parentNode.appendChild(editBtn);
        }

        var descDisplay = document.getElementById('descriptionDisplay');
        if (descDisplay) {
            var editBtn2 = document.createElement('button');
            editBtn2.className = 'btn btn-sm btn-secondary edit-inline-btn';
            editBtn2.textContent = '✏️ Edit Description';
            editBtn2.addEventListener('click', function() { showDescriptionEditor(); });
            descDisplay.parentNode.insertBefore(editBtn2, descDisplay.nextSibling);
        }
    }
}

function hideElement(id) {
    var el = document.getElementById(id);
    if (el) el.style.display = 'none';
}

// === Title Editing ===
function showTitleEditor() {
    document.getElementById('titleEditor').classList.remove('hidden');
    document.getElementById('titleInput').focus();
}

function cancelTitleEdit() {
    document.getElementById('titleEditor').classList.add('hidden');
    document.getElementById('titleInput').value = currentTitle || '';
}

function saveTitle() {
    if (typeof currentHash === 'undefined') return;
    var input = document.getElementById('titleInput');
    var newTitle = input.value.trim();

    api('/api/videos/' + currentHash + '/title', {
        method: 'PUT',
        body: JSON.stringify({ title: newTitle })
    }).then(function(video) {
        currentTitle = video.title;
        document.getElementById('videoTitle').textContent = video.title || video.filename;
        document.getElementById('titleEditor').classList.add('hidden');

        // Show/hide filename hint
        var hint = document.querySelector('.video-filename-hint');
        if (video.title) {
            if (!hint) {
                hint = document.createElement('div');
                hint.className = 'video-filename-hint';
                document.getElementById('videoTitle').parentNode.after(hint);
            }
            hint.textContent = '📄 ' + video.filename;
        } else if (hint) {
            hint.remove();
        }
    }).catch(function(err) {
        alert('Error: ' + err.message);
    });
}

// === Description Editing ===
function showDescriptionEditor() {
    document.getElementById('descriptionEditor').classList.remove('hidden');
    document.getElementById('descriptionInput').focus();
}

function cancelDescriptionEdit() {
    document.getElementById('descriptionEditor').classList.add('hidden');
    document.getElementById('descriptionInput').value = currentDescription || '';
}

function saveDescription() {
    if (typeof currentHash === 'undefined') return;
    var input = document.getElementById('descriptionInput');
    var newDesc = input.value;

    api('/api/videos/' + currentHash + '/description', {
        method: 'PUT',
        body: JSON.stringify({ description: newDesc })
    }).then(function(video) {
        currentDescription = video.description;
        renderDescription(video.description);
        document.getElementById('descriptionEditor').classList.add('hidden');
    }).catch(function(err) {
        alert('Error: ' + err.message);
    });
}

// === Simple Markdown Renderer ===
function renderDescription(md) {
    var container = document.getElementById('descriptionDisplay');
    if (!container) return;

    if (!md || md.trim() === '') {
        container.innerHTML = '<div style="color:var(--text-muted);font-style:italic">No description</div>';
        return;
    }

    container.innerHTML = simpleMarkdown(md);
}

function simpleMarkdown(text) {
    var lines = text.split('\n');
    var html = [];
    var inList = false;
    var inCode = false;

    for (var i = 0; i < lines.length; i++) {
        var line = lines[i];

        // Code blocks
        if (line.trim().startsWith('```')) {
            if (inCode) {
                html.push('</code></pre>');
                inCode = false;
            } else {
                if (inList) { html.push('</ul>'); inList = false; }
                html.push('<pre><code>');
                inCode = true;
            }
            continue;
        }
        if (inCode) {
            html.push(escapeHtml(line));
            html.push('\n');
            continue;
        }

        // Empty line
        if (line.trim() === '') {
            if (inList) { html.push('</ul>'); inList = false; }
            continue;
        }

        // Headers
        var headerMatch = line.match(/^(#{1,6})\s+(.*)/);
        if (headerMatch) {
            if (inList) { html.push('</ul>'); inList = false; }
            var level = headerMatch[1].length;
            html.push('<h' + level + '>' + inlineMarkdown(headerMatch[2]) + '</h' + level + '>');
            continue;
        }

        // Unordered list
        if (line.match(/^\s*[-*]\s+/)) {
            if (!inList) { html.push('<ul>'); inList = true; }
            html.push('<li>' + inlineMarkdown(line.replace(/^\s*[-*]\s+/, '')) + '</li>');
            continue;
        }

        // Paragraph
        if (inList) { html.push('</ul>'); inList = false; }
        html.push('<p>' + inlineMarkdown(line) + '</p>');
    }

    if (inList) html.push('</ul>');
    if (inCode) html.push('</code></pre>');

    return html.join('\n');
}

function inlineMarkdown(text) {
    // Escape HTML first
    text = escapeHtml(text);
    // Bold
    text = text.replace(/\*\*(.+?)\*\*/g, '<strong>\$1</strong>');
    // Italic
    text = text.replace(/\*(.+?)\*/g, '<em>\$1</em>');
    // Inline code
    text = text.replace(/`(.+?)`/g, '<code>\$1</code>');
    // Links
    text = text.replace(/$(.+?)$$(.+?)$/g, '<a href="\$2" target="_blank">\$1</a>');
    return text;
}

// === Autocomplete Engine ===
function setupAutocomplete(input, options) {
    options = options || {};
    var mode = options.mode || 'tag';
    var container = document.createElement('div');
    container.className = 'autocomplete-dropdown';
    container.style.display = 'none';
    input.parentNode.style.position = 'relative';
    input.parentNode.appendChild(container);
    var selectedIdx = -1;
    var currentMatches = [];

    function getEditingToken() {
        var val = input.value;
        var cursor = input.selectionStart || val.length;
        if (mode === 'search') {
            var before = val.substring(0, cursor);
            var match = before.match(/(?:^|\s)(tag:)([^\s]*)$/i);
            if (match) {
                return { prefix: match[2], start: before.length - match[2].length, end: cursor };
            }
            return null;
        } else {
            var segments = [], pos = 0, parts = val.split(',');
            for (var i = 0; i < parts.length; i++) {
                segments.push({ text: parts[i], start: pos, end: pos + parts[i].length });
                pos += parts[i].length + 1;
            }
            for (var j = 0; j < segments.length; j++) {
                if (cursor >= segments[j].start && cursor <= segments[j].end) {
                    var text = segments[j].text.trim();
                    var trimStart = segments[j].start + (segments[j].text.length - segments[j].text.trimStart().length);
                    return { prefix: text, start: trimStart, end: segments[j].end };
                }
            }
            return null;
        }
    }

    function showMatches(token) {
        if (!token || token.prefix.length === 0) { hide(); return; }
        var prefix = token.prefix.toLowerCase();
        currentMatches = allTagNames.filter(function(t) {
            return t.toLowerCase().indexOf(prefix) === 0 && t.toLowerCase() !== prefix;
        }).slice(0, 10);
        if (currentMatches.length === 0) { hide(); return; }
        selectedIdx = -1;
        container.innerHTML = '';
        currentMatches.forEach(function(tag, idx) {
            var item = document.createElement('div');
            item.className = 'autocomplete-item';
            item.textContent = tag;
            item.addEventListener('mousedown', function(e) { e.preventDefault(); pickMatch(token, idx); });
            container.appendChild(item);
        });
        container.style.display = 'block';
    }

    function pickMatch(token, idx) {
        var tag = currentMatches[idx];
        if (!tag) return;
        var val = input.value;
        if (mode === 'search') {
            input.value = val.substring(0, token.start) + tag + val.substring(token.end);
            var nc = token.start + tag.length;
            input.setSelectionRange(nc, nc);
        } else {
            var before = val.substring(0, token.start);
            var after = val.substring(token.end);
            var suffix = after.length > 0 ? '' : ', ';
            input.value = before + tag + suffix + after.trimStart();
            var nc = token.start + tag.length + suffix.length;
            input.setSelectionRange(nc, nc);
        }
        hide();
        input.focus();
    }

    function hide() { container.style.display = 'none'; currentMatches = []; selectedIdx = -1; }
    function highlightItem(idx) {
        container.querySelectorAll('.autocomplete-item').forEach(function(item, i) {
            item.classList.toggle('highlighted', i === idx);
        });
    }

    input.addEventListener('input', function() { showMatches(getEditingToken()); });
    input.addEventListener('keydown', function(e) {
        if (container.style.display === 'none') return;
        if (e.key === 'ArrowDown') { e.preventDefault(); selectedIdx = Math.min(selectedIdx + 1, currentMatches.length - 1); highlightItem(selectedIdx); }
        else if (e.key === 'ArrowUp') { e.preventDefault(); selectedIdx = Math.max(selectedIdx - 1, 0); highlightItem(selectedIdx); }
        else if (e.key === 'Enter' && selectedIdx >= 0) { e.preventDefault(); var t = getEditingToken(); if (t) pickMatch(t, selectedIdx); }
        else if (e.key === 'Escape') { hide(); }
        else if (e.key === 'Tab' && selectedIdx >= 0) { e.preventDefault(); var t = getEditingToken(); if (t) pickMatch(t, selectedIdx); }
    });
    input.addEventListener('blur', function() { setTimeout(hide, 150); });
}

function sanitizeTagInput(input) {
    input.addEventListener('input', function() {
        var val = input.value;
        var parts = val.split(',');
        var cleaned = parts.map(function(p) { return p.replace(/\s+/g, ''); });
        var newVal = cleaned.join(', ');
        if (newVal !== val) {
            var cursor = input.selectionStart;
            input.value = newVal;
            input.setSelectionRange(Math.min(cursor, newVal.length), Math.min(cursor, newVal.length));
        }
    });
}

// === Search ===
function doSearch() {
    var si = document.getElementById('searchInput');
    var val = si ? si.value.trim() : '';
    currentPage = 1;
    loadVideos(val || undefined);
}

function searchForTag(tag) {
    window.location.href = '/?search=' + encodeURIComponent('tag:' + tag);
}

function clearSearch() {
    var si = document.getElementById('searchInput');
    if (si) si.value = '';
    currentPage = 1;
    loadVideos();
}

// === Sorting ===
function setSort(field) {
    if (sortField === field) { sortDir = sortDir === 'asc' ? 'desc' : 'asc'; }
    else { sortField = field; sortDir = 'asc'; }
    currentPage = 1;
    renderVideoGrid(allVideos);
    updateSortButtons();
}

function updateSortButtons() {
    document.querySelectorAll('.sort-btn').forEach(function(btn) {
        var f = btn.dataset.sort;
        btn.classList.toggle('active', f === sortField);
        var arrow = btn.querySelector('.sort-arrow');
        if (arrow) arrow.textContent = f === sortField ? (sortDir === 'asc' ? ' ▲' : ' ▼') : '';
    });
}

function sortVideos(videos) {
    var sorted = videos.slice();
    sorted.sort(function(a, b) {
        var va, vb;
        switch (sortField) {
            case 'hash': va = a.hash; vb = b.hash; break;
            case 'filename': va = (a.title || a.filename || '').toLowerCase(); vb = (b.title || b.filename || '').toLowerCase(); break;
            case 'path': va = (a.path || '').toLowerCase(); vb = (b.path || '').toLowerCase(); break;
            case 'added': va = a.added_at || ''; vb = b.added_at || ''; break;
            case 'modified': va = a.modified_at || ''; vb = b.modified_at || ''; break;
            case 'duration': va = a.duration || 0; vb = b.duration || 0; break;
            case 'size': va = a.size || 0; vb = b.size || 0; break;
            default: va = (a.title || a.filename || '').toLowerCase(); vb = (b.title || b.filename || '').toLowerCase();
        }
        var cmp = typeof va === 'number' ? va - vb : (va < vb ? -1 : va > vb ? 1 : 0);
        return sortDir === 'desc' ? -cmp : cmp;
    });
    return sorted;
}

// === Pagination ===
function setPageSize(size) { pageSize = size; currentPage = 1; renderVideoGrid(allVideos); updatePageSizeButtons(); }
function updatePageSizeButtons() {
    document.querySelectorAll('.pagesize-btn').forEach(function(btn) {
        btn.classList.toggle('active', parseInt(btn.dataset.size) === pageSize);
    });
}
function goToPage(page) {
    var tp = Math.ceil(allVideos.length / pageSize);
    currentPage = Math.max(1, Math.min(page, tp));
    renderVideoGrid(allVideos);
    var grid = document.getElementById('videoGrid');
    if (grid) grid.scrollIntoView({ behavior: 'smooth', block: 'start' });
}

function renderPagination(total) {
    var container = document.getElementById('pagination');
    if (!container) return;
    var tp = Math.ceil(total / pageSize);
    if (tp <= 1) { container.innerHTML = ''; return; }
    container.innerHTML = '';

    var prev = document.createElement('button');
    prev.className = 'btn btn-sm btn-secondary'; prev.textContent = '← Prev';
    prev.disabled = currentPage <= 1;
    prev.addEventListener('click', function() { goToPage(currentPage - 1); });
    container.appendChild(prev);

    var max = 7, start = Math.max(1, currentPage - Math.floor(max / 2));
    var end = Math.min(tp, start + max - 1);
    if (end - start < max - 1) start = Math.max(1, end - max + 1);

    if (start > 1) { appendPageBtn(container, 1); if (start > 2) appendDots(container); }
    for (var i = start; i <= end; i++) appendPageBtn(container, i);
    if (end < tp) { if (end < tp - 1) appendDots(container); appendPageBtn(container, tp); }

    var next = document.createElement('button');
    next.className = 'btn btn-sm btn-secondary'; next.textContent = 'Next →';
    next.disabled = currentPage >= tp;
    next.addEventListener('click', function() { goToPage(currentPage + 1); });
    container.appendChild(next);
}

function appendPageBtn(c, p) {
    var b = document.createElement('button');
    b.className = 'btn btn-sm ' + (p === currentPage ? 'btn-primary' : 'btn-secondary');
    b.textContent = p;
    b.addEventListener('click', function() { goToPage(p); });
    c.appendChild(b);
}
function appendDots(c) {
    var s = document.createElement('span');
    s.className = 'page-dots'; s.textContent = '…';
    c.appendChild(s);
}

// === Video Loading ===
function loadVideos(searchQuery) {
    var grid = document.getElementById('videoGrid');
    if (!grid) return;
    grid.innerHTML = '<div class="loading">Loading videos</div>';
    var url = '/api/videos';
    if (searchQuery) url += '?search=' + encodeURIComponent(searchQuery);
    api(url).then(function(data) { allVideos = data || []; renderVideoGrid(allVideos); })
    .catch(function(err) {
        grid.innerHTML = '<div style="text-align:center;padding:3rem;color:#aaa">Error: ' + escapeHtml(err.message) + '</div>';
    });
}

function getDisplayName(v) {
    return v.title || v.filename || '';
}

function renderVideoGrid(videos) {
    var grid = document.getElementById('videoGrid');
    if (!grid) return;
    Object.keys(thumbIntervals).forEach(function(h) { clearInterval(thumbIntervals[h]); });
    thumbIntervals = {};
    var countDiv = document.getElementById('resultCount');
    if (!videos || videos.length === 0) {
        if (countDiv) countDiv.textContent = '';
        grid.innerHTML = '<div style="text-align:center;padding:3rem;color:#aaa">No videos found</div>';
        var pag = document.getElementById('pagination'); if (pag) pag.innerHTML = '';
        return;
    }
    var sorted = sortVideos(videos);
    var tp = Math.ceil(sorted.length / pageSize);
    if (currentPage > tp) currentPage = tp;
    if (currentPage < 1) currentPage = 1;
    var si = (currentPage - 1) * pageSize;
    var pv = sorted.slice(si, si + pageSize);
    if (countDiv) countDiv.textContent = (si + 1) + '–' + Math.min(si + pageSize, sorted.length) + ' of ' + sorted.length + ' videos';
    grid.innerHTML = '';

    pv.forEach(function(v) {
        var card = document.createElement('div');
        card.className = 'video-card' + (selectedHashes.has(v.hash) ? ' selected' : '');
        card.id = 'card-' + v.hash;
        var hasThumb = v.thumb_count && v.thumb_count > 0;
        var mainIdx = (v.main_thumb >= 0 && v.main_thumb < v.thumb_count) ? v.main_thumb : 0;
        var thumbUrl = hasThumb ? '/thumbs/' + v.hash + '/thumb_' + String(mainIdx).padStart(2, '0') + '.jpg' : '';
        var displayName = getDisplayName(v);

        var tagsHtml = '';
        if (v.tags && v.tags.length > 0) {
            tagsHtml = '<div class="card-tags">' + v.tags.slice(0, 5).map(function(t) {
                return '<span class="mini-tag clickable-tag" data-tag="' + escapeHtml(t) + '">' + escapeHtml(t) + '</span>';
            }).join('') + '</div>';
        }

        card.innerHTML =
            '<input type="checkbox" class="select-checkbox" ' + (selectedHashes.has(v.hash) ? 'checked' : '') + '>' +
            '<div class="thumb-container">' +
                (hasThumb ? '<img id="thumb-' + v.hash + '" src="' + thumbUrl + '" alt="" loading="lazy">' :
                    '<div style="position:absolute;top:50%;left:50%;transform:translate(-50%,-50%);color:#666">No thumbnail</div>') +
                '<span class="duration-badge">' + formatDuration(v.duration) + '</span>' +
            '</div>' +
            '<div class="card-body">' +
                '<div class="card-title" title="' + escapeHtml(displayName) + '">' + escapeHtml(displayName) + '</div>' +
                '<div class="card-meta">' + escapeHtml(v.directory) + '</div>' +
                tagsHtml +
            '</div>';

        card.querySelector('.select-checkbox').addEventListener('click', function(e) { e.stopPropagation(); toggleSelect(v.hash); });
        var tc = card.querySelector('.thumb-container');
        setupThumbInteraction(tc, v.hash, thumbUrl);
        var ct = card.querySelector('.card-title');
        if (ct) { ct.style.cursor = 'pointer'; ct.addEventListener('click', function() { window.location.href = '/video/' + v.hash; }); }
        var cm = card.querySelector('.card-meta');
        if (cm) { cm.style.cursor = 'pointer'; cm.addEventListener('click', function() { window.location.href = '/video/' + v.hash; }); }
        card.querySelectorAll('.clickable-tag').forEach(function(el) {
            el.addEventListener('click', function(e) { e.stopPropagation(); searchForTag(el.dataset.tag); });
        });
        grid.appendChild(card);
    });

    renderPagination(sorted.length);
    updateSortButtons();
    updatePageSizeButtons();
}

// === Thumbnail Interaction ===
function setupThumbInteraction(tc, hash, defaultThumb) {
    var cycling = false;
    tc.addEventListener('mouseenter', function() { if (!cycling) { cycling = true; startThumbCycleOnce(hash, function() { cycling = false; }); } });
    tc.addEventListener('mouseleave', function() { forceStopThumbCycle(hash, defaultThumb); cycling = false; });
    tc.addEventListener('click', function() { if (!tc._touchScrolled) window.location.href = '/video/' + hash; tc._touchScrolled = false; });
    var tsx = 0, tsy = 0; tc._touchScrolled = false; tc._touchCycled = false;
    tc.addEventListener('touchstart', function(e) { tsx = e.touches[0].clientX; tsy = e.touches[0].clientY; tc._touchScrolled = false; }, { passive: true });
    tc.addEventListener('touchmove', function(e) { if (Math.abs(e.touches[0].clientX - tsx) > 10 || Math.abs(e.touches[0].clientY - tsy) > 10) tc._touchScrolled = true; }, { passive: true });
    tc.addEventListener('touchend', function(e) {
        if (tc._touchScrolled) { if (!cycling) { cycling = true; startThumbCycleOnce(hash, function() { cycling = false; resetThumb(hash, defaultThumb); }); } return; }
        if (tc._touchCycled) { tc._touchCycled = false; window.location.href = '/video/' + hash; }
        else { tc._touchCycled = true; if (!cycling) { cycling = true; startThumbCycleOnce(hash, function() { cycling = false; tc._touchCycled = false; resetThumb(hash, defaultThumb); }); } setTimeout(function() { tc._touchCycled = false; }, 3000); }
        e.preventDefault();
    });
}

function loadThumbs(hash) {
    if (thumbCache[hash]) return Promise.resolve(thumbCache[hash]);
    return api('/api/thumbs/' + hash).then(function(t) { thumbCache[hash] = t || []; return thumbCache[hash]; }).catch(function() { thumbCache[hash] = []; return []; });
}

function startThumbCycleOnce(hash, onComplete) {
    loadThumbs(hash).then(function(thumbs) {
        if (thumbs.length <= 1) { if (onComplete) onComplete(); return; }
        var img = document.getElementById('thumb-' + hash);
        if (!img) { if (onComplete) onComplete(); return; }
        forceStopThumbCycle(hash);
        var idx = 0, shown = 0, count = thumbs.length;
        thumbIntervals[hash] = setInterval(function() {
            idx = (idx + 1) % count; img.src = thumbs[idx]; shown++;
            if (shown >= count) { clearInterval(thumbIntervals[hash]); delete thumbIntervals[hash]; if (onComplete) onComplete(); }
        }, 400);
    });
}

function forceStopThumbCycle(hash, dt) { if (thumbIntervals[hash]) { clearInterval(thumbIntervals[hash]); delete thumbIntervals[hash]; } if (dt) resetThumb(hash, dt); }
function resetThumb(hash, dt) { var img = document.getElementById('thumb-' + hash); if (img && dt) img.src = dt; }

// === Selection & Bulk ===
function toggleSelect(hash) {
    var card = document.getElementById('card-' + hash);
    var cb = card ? card.querySelector('.select-checkbox') : null;
    if (selectedHashes.has(hash)) { selectedHashes.delete(hash); if (card) card.classList.remove('selected'); if (cb) cb.checked = false; }
    else { selectedHashes.add(hash); if (card) card.classList.add('selected'); if (cb) cb.checked = true; }
    updateBulkBar();
}
function selectAll() { allVideos.forEach(function(v) { if (!selectedHashes.has(v.hash)) { selectedHashes.add(v.hash); var c = document.getElementById('card-' + v.hash); if (c) { c.classList.add('selected'); var cb = c.querySelector('.select-checkbox'); if (cb) cb.checked = true; } } }); updateBulkBar(); }
function selectPage() { var s = sortVideos(allVideos), si = (currentPage-1)*pageSize; s.slice(si, si+pageSize).forEach(function(v) { if (!selectedHashes.has(v.hash)) { selectedHashes.add(v.hash); var c = document.getElementById('card-'+v.hash); if(c){c.classList.add('selected'); var cb=c.querySelector('.select-checkbox'); if(cb) cb.checked=true;} } }); updateBulkBar(); }
function updateBulkBar() {
    var bar = document.getElementById('bulkBar'), count = document.getElementById('selectedCount');
    if (!bar || !count) return;
    count.textContent = selectedHashes.size;
    if (selectedHashes.size > 0 && !isReadOnly) bar.classList.remove('hidden'); else bar.classList.add('hidden');
}
function clearSelection() { selectedHashes.forEach(function(h) { var c = document.getElementById('card-'+h); if(c){c.classList.remove('selected'); var cb=c.querySelector('.select-checkbox'); if(cb) cb.checked=false;} }); selectedHashes.clear(); updateBulkBar(); }

function bulkAddTags() {
    var input = document.getElementById('bulkTagInput'); if (!input) return;
    var tags = input.value.split(',').map(function(t){return t.trim().replace(/\s+/g,'');}).filter(function(t){return t;});
    if (tags.length === 0 || selectedHashes.size === 0) return;
    api('/api/bulk/tags', { method: 'POST', body: JSON.stringify({ hashes: Array.from(selectedHashes), tags: tags, action: 'add' }) })
    .then(function() { input.value = ''; clearSelection(); loadAllTagNames(); doSearch(); })
    .catch(function(err) { alert('Error: ' + err.message); });
}
function bulkRemoveTags() {
    var input = document.getElementById('bulkTagInput'); if (!input) return;
    var tags = input.value.split(',').map(function(t){return t.trim().replace(/\s+/g,'');}).filter(function(t){return t;});
    if (tags.length === 0 || selectedHashes.size === 0) return;
    api('/api/bulk/tags', { method: 'POST', body: JSON.stringify({ hashes: Array.from(selectedHashes), tags: tags, action: 'remove' }) })
    .then(function() { input.value = ''; clearSelection(); loadAllTagNames(); doSearch(); })
    .catch(function(err) { alert('Error: ' + err.message); });
}

// === Single Video Tags ===
function addTagsToVideo() {
    if (typeof currentHash === 'undefined' || !currentHash) return;
    var input = document.getElementById('newTagInput'); if (!input) return;
    var tags = input.value.split(',').map(function(t){return t.trim().replace(/\s+/g,'');}).filter(function(t){return t;});
    if (tags.length === 0) return;
    api('/api/videos/'+currentHash+'/tags', { method:'POST', body:JSON.stringify({tags:tags}) })
    .then(function(v) { input.value=''; renderTagContainer(v.tags||[]); loadAllTagNames(); })
    .catch(function(err) { alert('Error: '+err.message); });
}
function removeTag(hash, tag) {
    api('/api/videos/'+hash+'/tags', { method:'DELETE', body:JSON.stringify({tags:[tag]}) })
    .then(function(v) { renderTagContainer(v.tags||[]); loadAllTagNames(); })
    .catch(function(err) { alert('Error: '+err.message); });
}
function renderTagContainer(tags) {
    var container = document.getElementById('tagContainer');
    if (!container || typeof currentHash === 'undefined') return;
    container.innerHTML = '';
    tags.forEach(function(t) {
        var span = document.createElement('span'); span.className = 'tag'; span.dataset.tag = t;
        var link = document.createElement('a'); link.className = 'tag-link';
        link.href = '/?search=' + encodeURIComponent('tag:' + t); link.textContent = t;
        link.addEventListener('click', function(e) { e.stopPropagation(); });
        span.appendChild(link);
        if (!isReadOnly) {
            var btn = document.createElement('button'); btn.className = 'tag-remove'; btn.innerHTML = '&times;';
            btn.addEventListener('click', function(e) { e.preventDefault(); e.stopPropagation(); removeTag(currentHash, t); });
            span.appendChild(btn);
        }
        container.appendChild(span);
    });
    if (tags.length > 0) loadSimilarVideos(currentHash, tags);
    else { var g = document.getElementById('similarGrid'); if (g) g.innerHTML = '<div style="color:var(--text-muted)">Add tags to see similar videos</div>'; }
}

// === Thumb Picker ===
function toggleThumbPicker() { var p = document.getElementById('thumbPicker'); if (p) p.classList.toggle('hidden'); }
function loadThumbPicker(hash) {
    var grid = document.getElementById('thumbPickerGrid'); if (!grid) return;
    loadThumbs(hash).then(function(thumbs) {
        if (!thumbs || thumbs.length === 0) { grid.innerHTML = '<div style="color:var(--text-muted)">No thumbnails</div>'; return; }
        grid.innerHTML = '';
        thumbs.forEach(function(url, idx) {
            var item = document.createElement('div');
            item.className = 'thumb-picker-item' + (idx === currentMainThumb ? ' selected' : '');
            item.innerHTML = '<img src="'+url+'" alt="Thumb '+idx+'"><span class="thumb-index">#'+idx+'</span>';
            item.addEventListener('click', function() { setMainThumb(hash, idx); });
            grid.appendChild(item);
        });
    });
}
function setMainThumb(hash, index) {
    api('/api/videos/'+hash+'/main-thumb', { method:'PUT', body:JSON.stringify({index:index}) })
    .then(function() {
        currentMainThumb = index;
        document.querySelectorAll('.thumb-picker-item').forEach(function(item,idx) { item.classList.toggle('selected', idx===index); });
        var p = document.getElementById('videoPlayer');
        if (p) p.poster = '/thumbs/'+hash+'/thumb_'+String(index).padStart(2,'0')+'.jpg';
    }).catch(function(err) { alert('Error: '+err.message); });
}

// === Similar Videos ===
function loadSimilarVideos(hash, tags) {
    var grid = document.getElementById('similarGrid'); if (!grid || !tags || tags.length === 0) return;
    grid.innerHTML = '<div class="loading">Finding similar</div>';
    var q = tags.map(function(t) { return 'tag:'+t; }).join(' OR ');
    api('/api/videos?search='+encodeURIComponent(q)).then(function(videos) {
        if (!videos) videos = [];
        var scored = [];
        videos.forEach(function(v) { if (v.hash===hash) return; var s=0; if(v.tags) v.tags.forEach(function(vt){if(tags.indexOf(vt)>=0)s++;}); scored.push({video:v,score:s}); });
        scored.sort(function(a,b){return b.score-a.score;});
        renderSimilarVideos(grid, scored.slice(0,12).map(function(s){return s.video;}));
    }).catch(function() { grid.innerHTML = '<div style="color:var(--text-muted)">Could not load similar videos</div>'; });
}
function renderSimilarVideos(grid, similar) {
    if (similar.length === 0) { grid.innerHTML = '<div style="color:var(--text-muted)">No similar videos found</div>'; return; }
    grid.innerHTML = '';
    similar.forEach(function(v) {
        var hasThumb = v.thumb_count && v.thumb_count > 0;
        var mainIdx = (v.main_thumb >= 0 && v.main_thumb < v.thumb_count) ? v.main_thumb : 0;
        var thumbUrl = hasThumb ? '/thumbs/'+v.hash+'/thumb_'+String(mainIdx).padStart(2,'0')+'.jpg' : '';
        var card = document.createElement('div'); card.className = 'video-card'; card.style.cursor = 'pointer';
        card.addEventListener('click', function() { window.location.href = '/video/'+v.hash; });
        card.innerHTML = '<div class="thumb-container">'+(hasThumb?'<img src="'+thumbUrl+'" alt="" loading="lazy">':'<div style="position:absolute;top:50%;left:50%;transform:translate(-50%,-50%);color:#666">No thumb</div>')+'<span class="duration-badge">'+formatDuration(v.duration)+'</span></div><div class="card-body"><div class="card-title">'+escapeHtml(getDisplayName(v))+'</div></div>';
        grid.appendChild(card);
    });
}

// === Tags Page ===
function loadTagList() {
    var c = document.getElementById('tagList'); if (!c) return;
    api('/api/tags').then(function(tags) {
        if (!tags||tags.length===0) { c.innerHTML='<div style="text-align:center;padding:3rem;color:#aaa">No tags yet</div>'; return; }
        c.innerHTML = '';
        tags.forEach(function(t) {
            var a = document.createElement('a'); a.href = '/?search='+encodeURIComponent('tag:'+t.name);
            a.className = 'tag-item'; a.innerHTML = escapeHtml(t.name)+' <span class="tag-count">'+t.count+'</span>';
            c.appendChild(a);
        });
    }).catch(function() { c.innerHTML='<div style="text-align:center;padding:3rem;color:#aaa">Error loading tags</div>'; });
}

// === Utilities ===
function formatDuration(s) { if(!s||s<=0) return '--:--'; var h=Math.floor(s/3600),m=Math.floor((s%3600)/60),sec=Math.floor(s%60); return h>0?h+':'+String(m).padStart(2,'0')+':'+String(sec).padStart(2,'0'):m+':'+String(sec).padStart(2,'0'); }
function escapeHtml(str) { if(!str) return ''; var d=document.createElement('div'); d.textContent=str; return d.innerHTML; }

// === Init ===
document.addEventListener('DOMContentLoaded', function() {
    isReadOnly = document.body.dataset.readonly === 'true';

    loadAllTagNames().then(function() {
        var si = document.getElementById('searchInput');
        if (si) { setupAutocomplete(si, {mode:'search'}); si.addEventListener('keyup', function(e){if(e.key==='Enter')doSearch();}); }
        var bi = document.getElementById('bulkTagInput');
        if (bi) { setupAutocomplete(bi, {mode:'tag'}); sanitizeTagInput(bi); }
        var ni = document.getElementById('newTagInput');
        if (ni) { setupAutocomplete(ni, {mode:'tag'}); sanitizeTagInput(ni); ni.addEventListener('keyup', function(e){if(e.key==='Enter')addTagsToVideo();}); }
    });

    if (window.location.pathname === '/') {
        var params = new URLSearchParams(window.location.search);
        var sp = params.get('search');
        var si = document.getElementById('searchInput');
        if (sp) { if (si) si.value = sp; loadVideos(sp); } else { loadVideos(); }
    }
});
