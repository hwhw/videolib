// === State ===
var allVideos = [];
var selectedHashes = new Set();
var thumbCache = {};
var thumbIntervals = {};

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

// === Search: always use the query engine ===
function doSearch() {
    var searchInput = document.getElementById('searchInput');
    var val = searchInput ? searchInput.value.trim() : '';

    if (!val) {
        loadVideos();
        return;
    }

    // Always use the search query engine
    loadVideos(val);
}

function searchForTag(tag) {
    var searchInput = document.getElementById('searchInput');
    var query = 'tag:' + tag;
    if (searchInput) searchInput.value = query;
    loadVideos(query);
}

function clearSearch() {
    var si = document.getElementById('searchInput');
    if (si) si.value = '';
    loadVideos();
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

    Object.keys(thumbIntervals).forEach(function(hash) {
        clearInterval(thumbIntervals[hash]);
    });
    thumbIntervals = {};

    var countDiv = document.getElementById('resultCount');
    if (countDiv) countDiv.textContent = (videos && videos.length > 0) ? videos.length + ' videos' : '';

    if (!videos || videos.length === 0) {
        grid.innerHTML = '<div style="text-align:center;padding:3rem;color:#aaa">No videos found</div>';
        return;
    }

    grid.innerHTML = '';

    videos.forEach(function(v) {
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

        // Thumbnail hover
        var thumbContainer = card.querySelector('.thumb-container');
        thumbContainer.addEventListener('mouseenter', function() {
            startThumbCycle(v.hash);
        });
        thumbContainer.addEventListener('mouseleave', function() {
            stopThumbCycle(v.hash, thumbUrl);
        });
        thumbContainer.addEventListener('click', function() {
            window.location.href = '/video/' + v.hash;
        });

        // Card body click -> navigate (but not on tags)
        var cardBody = card.querySelector('.card-body');
        var cardTitle = card.querySelector('.card-title');
        var cardMeta = card.querySelector('.card-meta');
        if (cardTitle) {
            cardTitle.addEventListener('click', function() {
                window.location.href = '/video/' + v.hash;
            });
        }
        if (cardMeta) {
            cardMeta.addEventListener('click', function() {
                window.location.href = '/video/' + v.hash;
            });
        }

        // Clickable tags in card
        var tagElements = card.querySelectorAll('.clickable-tag');
        tagElements.forEach(function(el) {
            el.addEventListener('click', function(e) {
                e.stopPropagation();
                searchForTag(el.dataset.tag);
            });
        });

        grid.appendChild(card);
    });
}

// === Thumbnail Cycling ===
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

function startThumbCycle(hash) {
    loadThumbs(hash).then(function(thumbs) {
        if (thumbs.length <= 1) return;
        var img = document.getElementById('thumb-' + hash);
        if (!img) return;
        var idx = 0;
        thumbIntervals[hash] = setInterval(function() {
            idx = (idx + 1) % thumbs.length;
            img.src = thumbs[idx];
        }, 500);
    });
}

function stopThumbCycle(hash, defaultThumb) {
    if (thumbIntervals[hash]) {
        clearInterval(thumbIntervals[hash]);
        delete thumbIntervals[hash];
    }
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
    var tags = input.value.split(',').map(function(t) { return t.trim(); }).filter(function(t) { return t; });
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
        doSearch();
    }).catch(function(err) {
        alert('Error: ' + err.message);
    });
}

function bulkRemoveTags() {
    var input = document.getElementById('bulkTagInput');
    if (!input) return;
    var tags = input.value.split(',').map(function(t) { return t.trim(); }).filter(function(t) { return t; });
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

    var tags = input.value.split(',').map(function(t) { return t.trim(); }).filter(function(t) { return t; });
    if (tags.length === 0) return;

    api('/api/videos/' + currentHash + '/tags', {
        method: 'POST',
        body: JSON.stringify({ tags: tags })
    }).then(function(video) {
        input.value = '';
        renderTagContainer(video.tags || []);
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
        span.className = 'tag clickable-tag';
        span.dataset.tag = t;

        var text = document.createTextNode(t + ' ');
        span.appendChild(text);

        var btn = document.createElement('button');
        btn.className = 'tag-remove';
        btn.innerHTML = '&times;';
        btn.addEventListener('click', function(e) {
            e.stopPropagation();
            removeTag(currentHash, t);
        });

        span.appendChild(btn);

        // Click tag to search for it
        span.addEventListener('click', function(e) {
            if (e.target === btn) return;
            window.location.href = '/?search=' + encodeURIComponent('tag:' + t);
        });

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

            item.addEventListener('click', function() {
                setMainThumb(hash, idx);
            });

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
            if (idx === index) {
                item.classList.add('selected');
            } else {
                item.classList.remove('selected');
            }
        });

        var player = document.getElementById('videoPlayer');
        if (player) {
            var thumbFile = 'thumb_' + String(index).padStart(2, '0') + '.jpg';
            player.poster = '/thumbs/' + hash + '/' + thumbFile;
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

    // Build OR query for all current tags
    var query = tags.map(function(t) {
        return 'tag:' + t;
    }).join(' OR ');

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
        var similar = scored.slice(0, 12).map(function(s) { return s.video; });
        renderSimilarVideos(grid, similar);
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
            ? '/thumbs/' + v.hash + '/thumb_' + String(mainIdx).padStart(2, '0') + '.jpg'
            : '';

        var card = document.createElement('div');
        card.className = 'video-card';
        card.style.cursor = 'pointer';
        card.addEventListener('click', function() {
            window.location.href = '/video/' + v.hash;
        });

        var thumbContent = hasThumb
            ? '<img src="' + thumbUrl + '" alt="" loading="lazy">'
            : '<div style="position:absolute;top:50%;left:50%;transform:translate(-50%,-50%);color:#666">No thumb</div>';

        card.innerHTML =
            '<div class="thumb-container">' +
                thumbContent +
                '<span class="duration-badge">' + formatDuration(v.duration) + '</span>' +
            '</div>' +
            '<div class="card-body">' +
                '<div class="card-title">' + escapeHtml(v.filename) + '</div>' +
            '</div>';

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
    if (h > 0) {
        return h + ':' + String(m).padStart(2, '0') + ':' + String(s).padStart(2, '0');
    }
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

    var searchInput = document.getElementById('searchInput');
    if (searchInput) {
        searchInput.addEventListener('keyup', function(e) {
            if (e.key === 'Enter') doSearch();
        });
    }
});
