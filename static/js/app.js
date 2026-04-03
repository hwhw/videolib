// === State ===
var allVideos = [];
var selectedHashes = new Set();
var thumbCache = {};
var thumbIntervals = {};
var allTagNames = [];

// Pagination & sorting state
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
                var prefix = match[2];
                var start = before.length - match[2].length;
                return { prefix: prefix, start: start, end: cursor, isTag: true };
            }
            return null;
        } else {
            var segments = [];
            var pos = 0;
            var parts = val.split(',');
            for (var i = 0; i < parts.length; i++) {
                var segStart = pos;
                var segEnd = pos + parts[i].length;
                segments.push({ text: parts[i], start: segStart, end: segEnd });
                pos = segEnd + 1;
            }
            for (var j = 0; j < segments.length; j++) {
                if (cursor >= segments[j].start && cursor <= segments[j].end) {
                    var text = segments[j].text.trim();
                    var trimStart = segments[j].start + (segments[j].text.length - segments[j].text.trimStart().length);
                    return { prefix: text, start: trimStart, end: segments[j].end, isTag: false };
                }
            }
            return null;
        }
    }

    function showMatches(token) {
        if (!token || token.prefix.length === 0) {
            hide();
            return;
        }
        var prefix = token.prefix.toLowerCase();
        currentMatches = allTagNames.filter(function(t) {
            return t.toLowerCase().indexOf(prefix) === 0 && t.toLowerCase() !== prefix;
        }).slice(0, 10);

        if (currentMatches.length === 0) {
            hide();
            return;
        }

        selectedIdx = -1;
        container.innerHTML = '';
        currentMatches.forEach(function(tag, idx) {
            var item = document.createElement('div');
            item.className = 'autocomplete-item';
            item.textContent = tag;
            item.addEventListener('mousedown', function(e) {
                e.preventDefault();
                pickMatch(token, idx);
            });
            container.appendChild(item);
        });
        container.style.display = 'block';
    }

    function pickMatch(token, idx) {
        var tag = currentMatches[idx];
        if (!tag) return;
        var val = input.value;

        if (mode === 'search') {
            var before = val.substring(0, token.start);
            var after = val.substring(token.end);
            input.value = before + tag + after;
            var newCursor = token.start + tag.length;
            input.setSelectionRange(newCursor, newCursor);
        } else {
            var before = val.substring(0, token.start);
            var after = val.substring(token.end);
            var suffix = after.length > 0 ? '' : ', ';
            input.value = before + tag + suffix + after.trimStart();
            var newCursor = token.start + tag.length + suffix.length;
            input.setSelectionRange(newCursor, newCursor);
        }

        hide();
        input.focus();
    }

    function hide() {
        container.style.display = 'none';
        currentMatches = [];
        selectedIdx = -1;
    }

    function highlightItem(idx) {
        var items = container.querySelectorAll('.autocomplete-item');
        items.forEach(function(item, i) {
            item.classList.toggle('highlighted', i === idx);
        });
    }

    input.addEventListener('input', function() {
        var token = getEditingToken();
        showMatches(token);
    });

    input.addEventListener('keydown', function(e) {
        if (container.style.display === 'none') return;
        if (e.key === 'ArrowDown') {
            e.preventDefault();
            selectedIdx = Math.min(selectedIdx + 1, currentMatches.length - 1);
            highlightItem(selectedIdx);
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            selectedIdx = Math.max(selectedIdx - 1, 0);
            highlightItem(selectedIdx);
        } else if (e.key === 'Enter' && selectedIdx >= 0) {
            e.preventDefault();
            var token = getEditingToken();
            if (token) pickMatch(token, selectedIdx);
        } else if (e.key === 'Escape') {
            hide();
        } else if (e.key === 'Tab' && selectedIdx >= 0) {
            e.preventDefault();
            var token = getEditingToken();
            if (token) pickMatch(token, selectedIdx);
        }
    });

    input.addEventListener('blur', function() {
        setTimeout(hide, 150);
    });

    return { hide: hide };
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
    var searchInput = document.getElementById('searchInput');
    var val = searchInput ? searchInput.value.trim() : '';
    currentPage = 1;
    if (!val) {
        loadVideos();
    } else {
        loadVideos(val);
    }
}

function searchForTag(tag) {
    var query = 'tag:' + tag;
    window.location.href = '/?search=' + encodeURIComponent(query);
}

function clearSearch() {
    var si = document.getElementById('searchInput');
    if (si) si.value = '';
    currentPage = 1;
    loadVideos();
}

// === Sorting ===
function setSort(field) {
    if (sortField === field) {
        sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
        sortField = field;
        sortDir = 'asc';
    }
    currentPage = 1;
    renderVideoGrid(allVideos);
    updateSortButtons();
}

function updateSortButtons() {
    var buttons = document.querySelectorAll('.sort-btn');
    buttons.forEach(function(btn) {
        var field = btn.dataset.sort;
        btn.classList.toggle('active', field === sortField);
        var arrow = btn.querySelector('.sort-arrow');
        if (arrow) {
            if (field === sortField) {
                arrow.textContent = sortDir === 'asc' ? ' ▲' : ' ▼';
            } else {
                arrow.textContent = '';
            }
        }
    });
}

function sortVideos(videos) {
    var sorted = videos.slice();
    sorted.sort(function(a, b) {
        var va, vb;
        switch (sortField) {
            case 'hash':
                va = a.hash;
                vb = b.hash;
                break;
            case 'filename':
                va = (a.filename || '').toLowerCase();
                vb = (b.filename || '').toLowerCase();
                break;
            case 'path':
                va = (a.path || '').toLowerCase();
                vb = (b.path || '').toLowerCase();
                break;
            case 'added':
                va = a.added_at || '';
                vb = b.added_at || '';
                break;
            case 'modified':
                va = a.modified_at || '';
                vb = b.modified_at || '';
                break;
            case 'duration':
                va = a.duration || 0;
                vb = b.duration || 0;
                break;
            case 'size':
                va = a.size || 0;
                vb = b.size || 0;
                break;
            default:
                va = (a.filename || '').toLowerCase();
                vb = (b.filename || '').toLowerCase();
        }
        var cmp;
        if (typeof va === 'number') {
            cmp = va - vb;
        } else {
            cmp = va < vb ? -1 : va > vb ? 1 : 0;
        }
        return sortDir === 'desc' ? -cmp : cmp;
    });
    return sorted;
}

// === Pagination ===
function setPageSize(size) {
    pageSize = size;
    currentPage = 1;
    renderVideoGrid(allVideos);
    updatePageSizeButtons();
}

function updatePageSizeButtons() {
    var buttons = document.querySelectorAll('.pagesize-btn');
    buttons.forEach(function(btn) {
        btn.classList.toggle('active', parseInt(btn.dataset.size) === pageSize);
    });
}

function goToPage(page) {
    var totalPages = Math.ceil(allVideos.length / pageSize);
    if (page < 1) page = 1;
    if (page > totalPages) page = totalPages;
    currentPage = page;
    renderVideoGrid(allVideos);
    // Scroll to top of grid
    var grid = document.getElementById('videoGrid');
    if (grid) grid.scrollIntoView({ behavior: 'smooth', block: 'start' });
}

function renderPagination(totalItems) {
    var container = document.getElementById('pagination');
    if (!container) return;

    var totalPages = Math.ceil(totalItems / pageSize);
    if (totalPages <= 1) {
        container.innerHTML = '';
        return;
    }

    container.innerHTML = '';

    // Prev
    var prev = document.createElement('button');
    prev.className = 'btn btn-sm btn-secondary';
    prev.textContent = '← Prev';
    prev.disabled = currentPage <= 1;
    prev.addEventListener('click', function() { goToPage(currentPage - 1); });
    container.appendChild(prev);

    // Page numbers
    var maxButtons = 7;
    var startPage = Math.max(1, currentPage - Math.floor(maxButtons / 2));
    var endPage = Math.min(totalPages, startPage + maxButtons - 1);
    if (endPage - startPage < maxButtons - 1) {
        startPage = Math.max(1, endPage - maxButtons + 1);
    }

    if (startPage > 1) {
        appendPageBtn(container, 1);
        if (startPage > 2) {
            var dots = document.createElement('span');
            dots.className = 'page-dots';
            dots.textContent = '…';
            container.appendChild(dots);
        }
    }

    for (var i = startPage; i <= endPage; i++) {
        appendPageBtn(container, i);
    }

    if (endPage < totalPages) {
        if (endPage < totalPages - 1) {
            var dots = document.createElement('span');
            dots.className = 'page-dots';
            dots.textContent = '…';
            container.appendChild(dots);
        }
        appendPageBtn(container, totalPages);
    }

    // Next
    var next = document.createElement('button');
    next.className = 'btn btn-sm btn-secondary';
    next.textContent = 'Next →';
    next.disabled = currentPage >= totalPages;
    next.addEventListener('click', function() { goToPage(currentPage + 1); });
    container.appendChild(next);
}

function appendPageBtn(container, page) {
    var btn = document.createElement('button');
    btn.className = 'btn btn-sm ' + (page === currentPage ? 'btn-primary' : 'btn-secondary');
    btn.textContent = page;
    btn.addEventListener('click', function() { goToPage(page); });
    container.appendChild(btn);
}

// === Video Loading ===
function loadVideos(searchQuery) {
    var grid = document.getElementById('videoGrid');
    if (!grid) return;

    grid.innerHTML = '<div class="loading">Loading videos</div>';

    var url = '/api/videos';
    if (searchQuery) {
        url += '?search=' + encodeURIComponent(searchQuery);
    }

    api(url).then(function(data) {
        allVideos = data || [];
        renderVideoGrid(allVideos);
    }).catch(function(err) {
        grid.innerHTML = '<div style="text-align:center;padding:3rem;color:#aaa">Error: ' + escapeHtml(err.message) + '</div>';
    });
}

function renderVideoGrid(videos) {
    var grid = document.getElementById('videoGrid');
    if (!grid) return;

    // Stop all thumb cycles
    Object.keys(thumbIntervals).forEach(function(hash) {
        clearInterval(thumbIntervals[hash]);
    });
    thumbIntervals = {};

    var countDiv = document.getElementById('resultCount');

    if (!videos || videos.length === 0) {
        if (countDiv) countDiv.textContent = '';
        grid.innerHTML = '<div style="text-align:center;padding:3rem;color:#aaa">No videos found</div>';
        var pag = document.getElementById('pagination');
        if (pag) pag.innerHTML = '';
        return;
    }

    // Sort
    var sorted = sortVideos(videos);

    // Paginate
    var totalPages = Math.ceil(sorted.length / pageSize);
    if (currentPage > totalPages) currentPage = totalPages;
    if (currentPage < 1) currentPage = 1;
    var startIdx = (currentPage - 1) * pageSize;
    var pageVideos = sorted.slice(startIdx, startIdx + pageSize);

    if (countDiv) {
        var rangeEnd = Math.min(startIdx + pageSize, sorted.length);
        countDiv.textContent = (startIdx + 1) + '–' + rangeEnd + ' of ' + sorted.length + ' videos';
    }

    grid.innerHTML = '';

    pageVideos.forEach(function(v) {
        var card = document.createElement('div');
        card.className = 'video-card' + (selectedHashes.has(v.hash) ? ' selected' : '');
        card.id = 'card-' + v.hash;
        card.dataset.hash = v.hash;

        var hasThumb = v.thumb_count && v.thumb_count > 0;
        var mainIdx = (v.main_thumb >= 0 && v.main_thumb < v.thumb_count) ? v.main_thumb : 0;
        var thumbUrl = hasThumb
            ? '/thumbs/' + v.hash + '/thumb_' + String(mainIdx).padStart(2, '0') + '.jpg'
            : '';
        var duration = formatDuration(v.duration);

        var tagsHtml = '';
        if (v.tags && v.tags.length > 0) {
            tagsHtml = '<div class="card-tags">' +
                v.tags.slice(0, 5).map(function(t) {
                    return '<span class="mini-tag clickable-tag" data-tag="' + escapeHtml(t) + '">' + escapeHtml(t) + '</span>';
                }).join('') + '</div>';
        }

        var thumbContent;
        if (hasThumb) {
            thumbContent = '<img id="thumb-' + v.hash + '" src="' + thumbUrl + '" alt="" loading="lazy">';
        } else {
            thumbContent = '<div style="position:absolute;top:50%;left:50%;transform:translate(-50%,-50%);color:#666">No thumbnail</div>';
        }

        card.innerHTML =
            '<input type="checkbox" class="select-checkbox" ' +
                (selectedHashes.has(v.hash) ? 'checked' : '') + '>' +
            '<div class="thumb-container">' +
                thumbContent +
                '<span class="duration-badge">' + duration + '</span>' +
            '</div>' +
            '<div class="card-body">' +
                '<div class="card-title" title="' + escapeHtml(v.filename) + '">' + escapeHtml(v.filename) + '</div>' +
                '<div class="card-meta">' + escapeHtml(v.directory) + '</div>' +
                tagsHtml +
            '</div>';

        // Checkbox
        var checkbox = card.querySelector('.select-checkbox');
        checkbox.addEventListener('click', function(e) {
            e.stopPropagation();
            toggleSelect(v.hash);
        });

        // Thumbnail cycling
        var thumbContainer = card.querySelector('.thumb-container');
        setupThumbInteraction(thumbContainer, v.hash, thumbUrl);

        // Navigation
        var cardTitle = card.querySelector('.card-title');
        var cardMeta = card.querySelector('.card-meta');
        if (cardTitle) {
            cardTitle.style.cursor = 'pointer';
            cardTitle.addEventListener('click', function() {
                window.location.href = '/video/' + v.hash;
            });
        }
        if (cardMeta) {
            cardMeta.style.cursor = 'pointer';
            cardMeta.addEventListener('click', function() {
                window.location.href = '/video/' + v.hash;
            });
        }

        // Clickable tags
        var tagElements = card.querySelectorAll('.clickable-tag');
        tagElements.forEach(function(el) {
            el.addEventListener('click', function(e) {
                e.stopPropagation();
                searchForTag(el.dataset.tag);
            });
        });

        grid.appendChild(card);
    });

    renderPagination(sorted.length);
    updateSortButtons();
    updatePageSizeButtons();
}

// === Thumbnail Interaction (mouse + touch) ===
function setupThumbInteraction(thumbContainer, hash, defaultThumb) {
    var cycling = false;

    // Desktop: mouseenter/mouseleave
    thumbContainer.addEventListener('mouseenter', function() {
        if (!cycling) {
            cycling = true;
            startThumbCycleOnce(hash, function() { cycling = false; });
        }
    });
    thumbContainer.addEventListener('mouseleave', function() {
        forceStopThumbCycle(hash, defaultThumb);
        cycling = false;
    });

    // Desktop: click navigates
    thumbContainer.addEventListener('click', function(e) {
        // Only navigate if not a touch-scroll situation
        if (!thumbContainer._touchScrolled) {
            window.location.href = '/video/' + hash;
        }
        thumbContainer._touchScrolled = false;
    });

    // Mobile: touch triggers cycle, second tap navigates
    var touchStartY = 0;
    var touchStartX = 0;
    thumbContainer._touchScrolled = false;
    thumbContainer._touchCycled = false;

    thumbContainer.addEventListener('touchstart', function(e) {
        touchStartX = e.touches[0].clientX;
        touchStartY = e.touches[0].clientY;
        thumbContainer._touchScrolled = false;
    }, { passive: true });

    thumbContainer.addEventListener('touchmove', function(e) {
        var dx = Math.abs(e.touches[0].clientX - touchStartX);
        var dy = Math.abs(e.touches[0].clientY - touchStartY);
        if (dx > 10 || dy > 10) {
            thumbContainer._touchScrolled = true;
        }
    }, { passive: true });

    thumbContainer.addEventListener('touchend', function(e) {
        if (thumbContainer._touchScrolled) {
            // It was a scroll — trigger thumb cycle
            if (!cycling) {
                cycling = true;
                startThumbCycleOnce(hash, function() {
                    cycling = false;
                    resetThumb(hash, defaultThumb);
                });
            }
            return;
        }

        // It was a tap
        if (thumbContainer._touchCycled) {
            // Second tap: navigate
            thumbContainer._touchCycled = false;
            window.location.href = '/video/' + hash;
        } else {
            // First tap: cycle thumbnails
            thumbContainer._touchCycled = true;
            if (!cycling) {
                cycling = true;
                startThumbCycleOnce(hash, function() {
                    cycling = false;
                    thumbContainer._touchCycled = false;
                    resetThumb(hash, defaultThumb);
                });
            }
            // Reset tap state after a timeout
            setTimeout(function() {
                thumbContainer._touchCycled = false;
            }, 3000);
        }
        e.preventDefault();
    });
}

// === Thumbnail Cycling (finite: one pass through all thumbs, then stop) ===
function loadThumbs(hash) {
    if (thumbCache[hash]) return Promise.resolve(thumbCache[hash]);
    return api('/api/thumbs/' + hash).then(function(thumbs) {
        thumbCache[hash] = thumbs || [];
        return thumbCache[hash];
    }).catch(function() {
        thumbCache[hash] = [];
        return [];
    });
}

function startThumbCycleOnce(hash, onComplete) {
    loadThumbs(hash).then(function(thumbs) {
        if (thumbs.length <= 1) {
            if (onComplete) onComplete();
            return;
        }

        var img = document.getElementById('thumb-' + hash);
        if (!img) {
            if (onComplete) onComplete();
            return;
        }

        var idx = 0;
        var count = thumbs.length;
        var shown = 0;

        // Clear any existing interval for this hash
        forceStopThumbCycle(hash);

        thumbIntervals[hash] = setInterval(function() {
            idx = (idx + 1) % count;
            img.src = thumbs[idx];
            shown++;

            // Stop after showing all thumbnails once
            if (shown >= count) {
                clearInterval(thumbIntervals[hash]);
                delete thumbIntervals[hash];
                if (onComplete) onComplete();
            }
        }, 400);
    });
}

function forceStopThumbCycle(hash, defaultThumb) {
    if (thumbIntervals[hash]) {
        clearInterval(thumbIntervals[hash]);
        delete thumbIntervals[hash];
    }
    if (defaultThumb) {
        resetThumb(hash, defaultThumb);
    }
}

function resetThumb(hash, defaultThumb) {
    var img = document.getElementById('thumb-' + hash);
    if (img && defaultThumb) {
        img.src = defaultThumb;
    }
}

// === Selection & Bulk Tagging ===
function toggleSelect(hash) {
    var card = document.getElementById('card-' + hash);
    var checkbox = card ? card.querySelector('.select-checkbox') : null;

    if (selectedHashes.has(hash)) {
        selectedHashes.delete(hash);
        if (card) card.classList.remove('selected');
        if (checkbox) checkbox.checked = false;
    } else {
        selectedHashes.add(hash);
        if (card) card.classList.add('selected');
        if (checkbox) checkbox.checked = true;
    }
    updateBulkBar();
}

function selectAll() {
    allVideos.forEach(function(v) {
        if (!selectedHashes.has(v.hash)) {
            selectedHashes.add(v.hash);
            var card = document.getElementById('card-' + v.hash);
            if (card) {
                card.classList.add('selected');
                var cb = card.querySelector('.select-checkbox');
                if (cb) cb.checked = true;
            }
        }
    });
    updateBulkBar();
}

function selectPage() {
    var sorted = sortVideos(allVideos);
    var startIdx = (currentPage - 1) * pageSize;
    var pageVideos = sorted.slice(startIdx, startIdx + pageSize);
    pageVideos.forEach(function(v) {
        if (!selectedHashes.has(v.hash)) {
            selectedHashes.add(v.hash);
            var card = document.getElementById('card-' + v.hash);
            if (card) {
                card.classList.add('selected');
                var cb = card.querySelector('.select-checkbox');
                if (cb) cb.checked = true;
            }
        }
    });
    updateBulkBar();
}

function updateBulkBar() {
    var bar = document.getElementById('bulkBar');
    var count = document.getElementById('selectedCount');
    if (!bar || !count) return;

    count.textContent = selectedHashes.size;
    if (selectedHashes.size > 0) {
        bar.classList.remove('hidden');
    } else {
        bar.classList.add('hidden');
    }
}

function clearSelection() {
    selectedHashes.forEach(function(hash) {
        var card = document.getElementById('card-' + hash);
        if (card) {
            card.classList.remove('selected');
            var cb = card.querySelector('.select-checkbox');
            if (cb) cb.checked = false;
        }
    });
    selectedHashes.clear();
    updateBulkBar();
}

function bulkAddTags() {
    var input = document.getElementById('bulkTagInput');
    if (!input) return;
    var tags = input.value.split(',').map(function(t) { return t.trim().replace(/\s+/g, ''); }).filter(function(t) { return t; });
    if (tags.length === 0 || selectedHashes.size === 0) return;

    api('/api/bulk/tags', {
        method: 'POST',
        body: JSON.stringify({
            hashes: Array.from(selectedHashes),
            tags: tags,
            action: 'add'
        })
    }).then(function() {
        input.value = '';
        clearSelection();
        loadAllTagNames();
        doSearch();
    }).catch(function(err) {
        alert('Error: ' + err.message);
    });
}

function bulkRemoveTags() {
    var input = document.getElementById('bulkTagInput');
    if (!input) return;
    var tags = input.value.split(',').map(function(t) { return t.trim().replace(/\s+/g, ''); }).filter(function(t) { return t; });
    if (tags.length === 0 || selectedHashes.size === 0) return;

    api('/api/bulk/tags', {
        method: 'POST',
        body: JSON.stringify({
            hashes: Array.from(selectedHashes),
            tags: tags,
            action: 'remove'
        })
    }).then(function() {
        input.value = '';
        clearSelection();
        loadAllTagNames();
        doSearch();
    }).catch(function(err) {
        alert('Error: ' + err.message);
    });
}

// === Single Video Tag Editing ===
function addTagsToVideo() {
    if (typeof currentHash === 'undefined' || !currentHash) return;
    var input = document.getElementById('newTagInput');
    if (!input) return;

    var tags = input.value.split(',').map(function(t) { return t.trim().replace(/\s+/g, ''); }).filter(function(t) { return t; });
    if (tags.length === 0) return;

    api('/api/videos/' + currentHash + '/tags', {
        method: 'POST',
        body: JSON.stringify({ tags: tags })
    }).then(function(video) {
        input.value = '';
        renderTagContainer(video.tags || []);
        loadAllTagNames();
    }).catch(function(err) {
        alert('Error: ' + err.message);
    });
}

function removeTag(hash, tag) {
    api('/api/videos/' + hash + '/tags', {
        method: 'DELETE',
        body: JSON.stringify({ tags: [tag] })
    }).then(function(video) {
        renderTagContainer(video.tags || []);
        loadAllTagNames();
    }).catch(function(err) {
        alert('Error: ' + err.message);
    });
}

function renderTagContainer(tags) {
    var container = document.getElementById('tagContainer');
    if (!container || typeof currentHash === 'undefined') return;

    container.innerHTML = '';
    tags.forEach(function(t) {
        var span = document.createElement('span');
        span.className = 'tag';
        span.dataset.tag = t;

        var link = document.createElement('a');
        link.className = 'tag-link';
        link.href = '/?search=' + encodeURIComponent('tag:' + t);
        link.textContent = t;
        link.addEventListener('click', function(e) { e.stopPropagation(); });

        var btn = document.createElement('button');
        btn.className = 'tag-remove';
        btn.innerHTML = '&times;';
        btn.addEventListener('click', function(e) {
            e.preventDefault();
            e.stopPropagation();
            removeTag(currentHash, t);
        });

        span.appendChild(link);
        span.appendChild(btn);
        container.appendChild(span);
    });

    if (tags.length > 0) {
        loadSimilarVideos(currentHash, tags);
    } else {
        var grid = document.getElementById('similarGrid');
        if (grid) grid.innerHTML = '<div style="color:var(--text-muted)">Add tags to see similar videos</div>';
    }
}

// === Thumbnail Picker ===
function toggleThumbPicker() {
    var picker = document.getElementById('thumbPicker');
    if (picker) picker.classList.toggle('hidden');
}

function loadThumbPicker(hash) {
    var grid = document.getElementById('thumbPickerGrid');
    if (!grid) return;

    loadThumbs(hash).then(function(thumbs) {
        if (!thumbs || thumbs.length === 0) {
            grid.innerHTML = '<div style="color:var(--text-muted)">No thumbnails available</div>';
            return;
        }

        grid.innerHTML = '';
        thumbs.forEach(function(url, idx) {
            var item = document.createElement('div');
            item.className = 'thumb-picker-item' + (idx === currentMainThumb ? ' selected' : '');
            item.innerHTML =
                '<img src="' + url + '" alt="Thumbnail ' + idx + '">' +
                '<span class="thumb-index">#' + idx + '</span>';
            item.addEventListener('click', function() { setMainThumb(hash, idx); });
            grid.appendChild(item);
        });
    });
}

function setMainThumb(hash, index) {
    api('/api/videos/' + hash + '/main-thumb', {
        method: 'PUT',
        body: JSON.stringify({ index: index })
    }).then(function() {
        currentMainThumb = index;
        var items = document.querySelectorAll('.thumb-picker-item');
        items.forEach(function(item, idx) {
            item.classList.toggle('selected', idx === index);
        });
        var player = document.getElementById('videoPlayer');
        if (player) {
            player.poster = '/thumbs/' + hash + '/thumb_' + String(index).padStart(2, '0') + '.jpg';
        }
    }).catch(function(err) {
        alert('Error setting thumbnail: ' + err.message);
    });
}

// === Similar Videos ===
function loadSimilarVideos(hash, tags) {
    var grid = document.getElementById('similarGrid');
    if (!grid || !tags || tags.length === 0) return;

    grid.innerHTML = '<div class="loading">Finding similar</div>';

    var query = tags.map(function(t) { return 'tag:' + t; }).join(' OR ');

    api('/api/videos?search=' + encodeURIComponent(query)).then(function(videos) {
        if (!videos) videos = [];
        var scored = [];
        videos.forEach(function(v) {
            if (v.hash === hash) return;
            var shared = 0;
            if (v.tags) {
                v.tags.forEach(function(vt) {
                    if (tags.indexOf(vt) >= 0) shared++;
                });
            }
            scored.push({ video: v, score: shared });
        });
        scored.sort(function(a, b) { return b.score - a.score; });
        renderSimilarVideos(grid, scored.slice(0, 12).map(function(s) { return s.video; }));
    }).catch(function() {
        grid.innerHTML = '<div style="color:var(--text-muted)">Could not load similar videos</div>';
    });
}

function renderSimilarVideos(grid, similar) {
    if (similar.length === 0) {
        grid.innerHTML = '<div style="color:var(--text-muted)">No similar videos found</div>';
        return;
    }
    grid.innerHTML = '';
    similar.forEach(function(v) {
        var hasThumb = v.thumb_count && v.thumb_count > 0;
        var mainIdx = (v.main_thumb >= 0 && v.main_thumb < v.thumb_count) ? v.main_thumb : 0;
        var thumbUrl = hasThumb
            ? '/thumbs/' + v.hash + '/thumb_' + String(mainIdx).padStart(2, '0') + '.jpg' : '';
        var card = document.createElement('div');
        card.className = 'video-card';
        card.style.cursor = 'pointer';
        card.addEventListener('click', function() { window.location.href = '/video/' + v.hash; });
        card.innerHTML =
            '<div class="thumb-container">' +
                (hasThumb ? '<img src="' + thumbUrl + '" alt="" loading="lazy">' :
                    '<div style="position:absolute;top:50%;left:50%;transform:translate(-50%,-50%);color:#666">No thumb</div>') +
                '<span class="duration-badge">' + formatDuration(v.duration) + '</span>' +
            '</div>' +
            '<div class="card-body"><div class="card-title">' + escapeHtml(v.filename) + '</div></div>';
        grid.appendChild(card);
    });
}

// === Tags Page ===
function loadTagList() {
    var container = document.getElementById('tagList');
    if (!container) return;

    api('/api/tags').then(function(tags) {
        if (!tags || tags.length === 0) {
            container.innerHTML = '<div style="text-align:center;padding:3rem;color:#aaa">No tags yet</div>';
            return;
        }
        container.innerHTML = '';
        tags.forEach(function(t) {
            var a = document.createElement('a');
            a.href = '/?search=' + encodeURIComponent('tag:' + t.name);
            a.className = 'tag-item';
            a.innerHTML = escapeHtml(t.name) + ' <span class="tag-count">' + t.count + '</span>';
            container.appendChild(a);
        });
    }).catch(function() {
        container.innerHTML = '<div style="text-align:center;padding:3rem;color:#aaa">Error loading tags</div>';
    });
}

// === Utilities ===
function formatDuration(seconds) {
    if (!seconds || seconds <= 0) return '--:--';
    var h = Math.floor(seconds / 3600);
    var m = Math.floor((seconds % 3600) / 60);
    var s = Math.floor(seconds % 60);
    if (h > 0) return h + ':' + String(m).padStart(2, '0') + ':' + String(s).padStart(2, '0');
    return m + ':' + String(s).padStart(2, '0');
}

function escapeHtml(str) {
    if (!str) return '';
    var div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

// === Init ===
document.addEventListener('DOMContentLoaded', function() {
    loadAllTagNames().then(function() {
        var searchInput = document.getElementById('searchInput');
        if (searchInput) {
            setupAutocomplete(searchInput, { mode: 'search' });
            searchInput.addEventListener('keyup', function(e) {
                if (e.key === 'Enter') doSearch();
            });
        }

        var bulkInput = document.getElementById('bulkTagInput');
        if (bulkInput) {
            setupAutocomplete(bulkInput, { mode: 'tag' });
            sanitizeTagInput(bulkInput);
        }

        var newTagInput = document.getElementById('newTagInput');
        if (newTagInput) {
            setupAutocomplete(newTagInput, { mode: 'tag' });
            sanitizeTagInput(newTagInput);
            newTagInput.addEventListener('keyup', function(e) {
                if (e.key === 'Enter') addTagsToVideo();
            });
        }
    });

    if (window.location.pathname === '/') {
        var params = new URLSearchParams(window.location.search);
        var searchParam = params.get('search');
        var searchInput = document.getElementById('searchInput');

        if (searchParam) {
            if (searchInput) searchInput.value = searchParam;
            loadVideos(searchParam);
        } else {
            loadVideos();
        }
    }
});
