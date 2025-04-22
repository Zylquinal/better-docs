(() => {
    const $ = (sel, ctx = document) => ctx == null ? null : ctx.querySelector(sel);
    const $$ = (sel, ctx = document) => Array.from(ctx.querySelectorAll(sel));
    const on = (el, evt, cb, opts) => el.addEventListener(evt, cb, opts);
    const debounce = (fn, ms = 300) => {
        let id;
        return (...args) => {
            clearTimeout(id);
            id = setTimeout(() => fn(...args), ms);
        };
    };

    const getValueSetter = el => Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), 'value').set;
    const getValueGetter = el => Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), 'value').get;

    const ls = {
        get: key => localStorage.getItem(key),
        set: (key, val) => localStorage.setItem(key, val),
        del: key => localStorage.removeItem(key)
    };

    let currentSpec;
    let specOptions = [];
    let activeFilters = [];
    let lastRaSearchResult = null;
    let tryItObserver = null;

    const els = {
        specSelector: $('#spec-selector'),
        viewer: $('#viewer-container'),

        // Filter modal
        filterModal: $('#filter-modal'),
        filterBtn: $('#spec-filter-button'),
        filterList: $('#filter-list'),
        filterClose: $('#filter-close'),
        filterApply: $('#filter-apply'),

        // Search
        searchBar: $('#search-bar'),
        results: $('#search-results'),
        searchModal: $('#search-modal'),
        closeSearchModal: $('#close-search-modal'),
        prevPage: $('#prev-page'),
        nextPage: $('#next-page'),
        searchList: $('#search-list'),
        pageInfo: $('#page-info'),

        // RA Search
        raBtn: $('#ra-button'),
        raModal: $('#ra-modal'),
        raInput: $('#ra-input'),
        raStatus: $('#ra-status'),
        raResponse: $('#ra-response'),
        raSubmit: $('#ra-submit'),
        raNotifications: $('#ra-notifications'),
        raAction: $('#ra-action'),
        raClose: $('#ra-close')
    };

    async function loadSpecs() {
        const res = await fetch('/api/specs');
        const specs = await res.json();

        specOptions = specs;

        els.specSelector.innerHTML = '';
        specs.forEach(s => els.specSelector.append(new Option(s.displayName, s.name)));

        const [, initialFromUrl] = window.location.pathname.match(/^\/specs\/([^/]+)/) || [];
        const initial = initialFromUrl || specs[0].name;

        els.specSelector.value = initial;
        currentSpec = initial;

        await renderSpec(initial);
    }

    async function renderSpec(name) {
        currentSpec = name;

        els.viewer.innerHTML = '';
        const el = document.createElement('elements-api');
        el.apiDescriptionUrl = `/api/specs/${name}`;
        el.router            = 'hash';
        els.viewer.append(el);

        observeTryIt(name);
        applyRaPayload();
    }


    const getOperationKey = () => {
        const [, op] = window.location.hash.match(/\/operations\/([^/?]+)/) || [];
        return op || 'default';
    };

    function observeTryIt(specName) {
        tryItObserver?.disconnect();
        tryItObserver = new MutationObserver(() => attachTryIt(specName));
        tryItObserver.observe(els.viewer, {childList: true, subtree: true});
    }

    function attachTryIt(specName) {
        const panel = $('.TryItPanel', els.viewer);
        if (!panel) return;

        const opKey = getOperationKey();
        if (panel.__eOpKey === opKey) return;
        panel.__eOpKey = opKey;

        // Save default values for params and body
        const inputs = $$('input[aria-label]', panel);
        inputs.forEach(input => {
            // Force clear
            getValueSetter(input).call(input, '');
            input.dispatchEvent(new Event('input', {bubbles: true}));
            input.dispatchEvent(new Event('change', {bubbles: true}));
        });

        panel.__defaultParams = inputs.reduce((acc, input) => {
            acc[input.getAttribute('aria-label')] = input.defaultValue || '';
            return acc;
        }, {});

        const ta = $('.sl-code-editor textarea', panel);
        if (ta) panel.__defaultBody = getValueGetter(ta).call(ta);

        injectResetButton(panel, specName, opKey, inputs, ta);

        // Param (input) persistence
        inputs.forEach(input => {
            const key = input.getAttribute('aria-label');
            const storageKey = `el-param:${specName}:${opKey}:${key}`;
            const saved = ls.get(storageKey);
            input.value = saved ?? panel.__defaultParams[key];
            input.dispatchEvent(new Event('input', {bubbles: true}));
            input.dispatchEvent(new Event('change', {bubbles: true}));
            on(input, 'input', () => ls.set(storageKey, input.value));
        });

        // ------------------------- Persistence (body) ---------------------------
        if (ta) {
            const bodyKey = `el-body:${specName}:${opKey}`;
            const savedBody = ls.get(bodyKey);
            const toSet = savedBody ?? panel.__defaultBody ?? '';
            getValueSetter(ta).call(ta, toSet);
            ta.dispatchEvent(new Event('input', {bubbles: true}));
            ta.dispatchEvent(new Event('change', {bubbles: true}));
            on(ta, 'input', () => ls.set(bodyKey, ta.value));
        }
    }

    function injectResetButton(panel, specName, opKey, inputs, ta) {
        const btnGroup = $('.sl-stack.sl-stack--horizontal', panel);
        if (!btnGroup) return;
        if (btnGroup.querySelector('.reset-btn')) return; // avoid dupes

        const resetBtn = document.createElement('button');
        resetBtn.type = 'button';
        resetBtn.textContent = 'Reset';
        resetBtn.className = [
            'reset-btn',
            'sl-button',
            'sl-form-group-border',
            'sl-h-sm',
            'sl-text-base',
            'sl-font-medium',
            'sl-px-1.5',
            'sl-bg-danger',
            'hover:sl-bg-danger-dark',
            'active:sl-bg-danger-darker',
            'sl-text-on-danger',
            'sl-rounded',
            'sl-border-transparent'
        ].join(' ');

        btnGroup.insertBefore(resetBtn, btnGroup.children[1]);

        on(resetBtn, 'click', () => {
            // Params (Request)
            inputs.forEach(input => {
                const key = input.getAttribute('aria-label');
                input.value = panel.__defaultParams[key] || '';
                input.dispatchEvent(new Event('input', {bubbles: true}));
                input.dispatchEvent(new Event('change', {bubbles: true}));
                ls.del(`el-param:${specName}:${opKey}:${key}`);
            });

            // Body (Request)
            if (ta) {
                getValueSetter(ta).call(ta, panel.__defaultBody || '');
                ta.dispatchEvent(new Event('input', {bubbles: true}));
                ta.dispatchEvent(new Event('change', {bubbles: true}));
                ls.del(`el-body:${specName}:${opKey}`);
            }
        });
    }

    //  Spec filter modal
    function openFilterModal() {
        els.filterList.innerHTML = '';

        // All‑specs checkbox
        const allLabel = document.createElement('label');
        allLabel.innerHTML = '<input type="checkbox" id="all-specs" /> All specs';
        els.filterList.append(allLabel);
        const allCheckbox = $('#all-specs');
        allCheckbox.checked = activeFilters.length === 0;

        // Individual specs
        specOptions.forEach(s => {
            const lbl = document.createElement('label');
            lbl.style.display = 'block';
            lbl.innerHTML = `<input type="checkbox" value="${s.name}" /> ${s.displayName}`;
            const cb = $('input', lbl);
            cb.checked = activeFilters.length === 0 || activeFilters.includes(s.name);
            els.filterList.append(lbl);
        });

        on(allCheckbox, 'change', () => {
            $$('#filter-list input[type=checkbox]').forEach(i => {
                i.checked = allCheckbox.checked;
            });
        });

        $$('#filter-list input[type=checkbox]').forEach(cb => {
            if (cb.id !== 'all-specs') {
                on(cb, 'change', () => {
                    const boxes = $$('#filter-list input[type=checkbox]');
                    allCheckbox.checked = boxes.slice(1).every(i => i.checked);
                });
            }
        });

        els.filterModal.classList.add('open');
        els.results.style.display = 'none';
    }

    function applyFilters() {
        const checks = $$('#filter-list input[type=checkbox]:checked').map(i => i.value);
        activeFilters = checks.includes('*') ? [] : checks.filter(v => v !== '*');
        els.filterModal.classList.remove('open');
    }

    // ---------------------------------------------------------------------------
    //  Search (dropdown + modal pagination)
    // ---------------------------------------------------------------------------
    let allResults = [];
    let totalHits = 0;
    let page = 0;
    const perPage = 10;

    async function doSearch(offset = 0) {
        const q = els.searchBar.value.trim();
        if (!q) return els.results.style.display = 'none';

        const params = new URLSearchParams({q, limit: perPage, offset});
        activeFilters.forEach(s => params.append('spec', s));

        const res = await fetch(`/search?${params}`);
        const json = await res.json();

        ({results: allResults, total: totalHits} = json);
        page = offset / perPage;

        populateDropdown();
    }

    function populateDropdown() {
        els.results.innerHTML = '';

        allResults.forEach(r => els.results.append(buildResultItem(r)));

        if (totalHits > perPage) {
            const showMore = document.createElement('div');
            showMore.className = 'show-more';
            showMore.textContent = 'Show more…';
            on(showMore, 'click', () => {
                els.searchModal.classList.add('open');
                populateModal();
                els.results.style.display = 'none';
            });
            els.results.append(showMore);
        }

        els.results.style.display = 'block';
    }

    function buildResultItem(r) {
        const d = document.createElement('div');
        d.className = 'result-item';
        d.innerHTML = `
          <div class="result-spec">Spec: ${r.SpecName}</div>
          <div class="result-title">${r.Method} ${r.OperationID}</div>
          <div class="result-template">${r.Template}</div>
          <div class="result-desc">${r.Description}</div>
        `;
        on(d, 'click', () => {
            window.location.href = `${window.location.origin}/specs/${r.SpecName}/#/operations/${r.OperationID}`;
        });
        return d;
    }

    async function changePage(delta) {
        const newPage = page + delta;
        if (newPage < 0 || newPage * perPage >= totalHits) return;
        await doSearch(newPage * perPage);
        populateModal();
    }

    function populateModal() {
        els.searchList.innerHTML = '';
        els.pageInfo.textContent = `Page ${page + 1} of ${Math.ceil(totalHits / perPage)}`;
        allResults.forEach(r => els.searchList.append(buildResultItem(r)));
    }

    // ---------------------------------------------------------------------------
    //  RA Search
    // ---------------------------------------------------------------------------
    async function handleRaSearch() {
        els.raResponse.value = '';
        els.raStatus.textContent = '';

        if (els.raSubmit.textContent === 'Open Spec' && lastRaSearchResult) {
            await openRaSpec();
            return;
        }

        const logText = els.raInput.value;
        try {
            const res = await fetch('/raSearch', {method: 'POST', body: logText});
            if (!res.ok) throw new Error(await res.text());

            lastRaSearchResult = await res.json();
            els.raSubmit.textContent = 'Open Spec';
            notify(`✔ Found operation “${lastRaSearchResult.operationId}” at “${lastRaSearchResult.specName}”`, 'success');
        } catch (err) {
            els.raStatus.textContent = `Error: ${err.message}`;
            notify('⚠️ No matching operation found', 'error');
        }
    }

    async function openRaSpec() {
        const {specName, operationId, parsedInfo} = lastRaSearchResult;
        const {PathParams = {}, Params = {}, Body: b64Body} = parsedInfo;
        const body = b64Body ? atob(b64Body) : null;

        Object.entries(Params).forEach(([key, [val]]) => {
            ls.set(`el-param:${specName}:${operationId}:${key}`, val);
        });
        Object.entries(PathParams).forEach(([key, val]) => {
            ls.set(`el-param:${specName}:${operationId}:${key}`, val);
        });

        if (body !== null) {
            let formatted = body;
            try {
                formatted = JSON.stringify(JSON.parse(body), null, 2);
            } catch {
            }
            ls.set(`el-body:${specName}:${operationId}`, formatted);
        }

        ls.set('raParsed', JSON.stringify({params: Params, body}));

        els.specSelector.value = specName;

        window.history.replaceState(
            null,
            '',
            `/specs/${specName}/#/operations/${operationId}`
        );

        await renderSpec(specName);


        lastRaSearchResult = null;
        els.raSubmit.textContent = 'Search';
        els.raModal.classList.remove('open');
    }

    async function raAction() {
        els.raNotifications.innerHTML = '';
        els.raStatus.textContent = '';
        els.raResponse.value = '';
        const logText = els.raInput.value;
        const res = await fetch('/action', {method: 'POST', body: logText});
        if (res.ok) {
            els.raResponse.value = await res.text();
        } else {
            const err = await res.text();
            els.raStatus.textContent = `Error: ${err}`;
            addNotification(`⚠️ ${err}`, 'error');
        }
    }

    // Apply RA payload (called within renderSpec)
    function applyRaPayload() {
        const raw = ls.get('raParsed');
        if (!raw) return;
        const {params, body} = JSON.parse(raw);
        ls.del('raParsed');

        const fillParams = () => {
            const panel = $('.TryItPanel', els.viewer);
            if (!panel) return;
            Object.entries(params).forEach(([key, [val]]) => {
                const input = $(`input[aria-label="${key}"]`, panel);
                if (input) {
                    input.value = val;
                    input.dispatchEvent(new Event('input', {bubbles: true}));
                    input.dispatchEvent(new Event('change', {bubbles: true}));
                }
            });
        };

        fillParams();
        setTimeout(fillParams, 500); // in case inputs mount late (at least it works) :D

        if (!body) return;

        let formatted = body;
        try {
            formatted = JSON.stringify(JSON.parse(body), null, 2);
        } catch {
            console.warn('RA body not valid JSON, skipping pretty‑print');
        }

        const tryFillBody = () => {
            const panel = $('.TryItPanel', els.viewer);
            if (!panel) return false;

            const ta = $('.sl-code-editor textarea', panel);
            if (!ta) return false;

            getValueSetter(ta).call(ta, formatted);
            ta.dispatchEvent(new Event('input', {bubbles: true}));
            ta.dispatchEvent(new Event('change', {bubbles: true}));
            return true;
        };


        if (!tryFillBody()) {
            let attempts = 0;
            const poll = setInterval(() => {
                if (tryFillBody() || ++attempts > 50) clearInterval(poll);
            }, 200);
        }
    }

    // ---------------------------------------------------------------------------
    //  Notifications
    // ---------------------------------------------------------------------------
    function notify(msg, type = 'success', duration = 3000) {
        const toast = document.createElement('div');
        toast.className = `notification ${type}`;
        toast.textContent = msg;
        els.raNotifications.append(toast);
        setTimeout(() => {
            toast.classList.add('fade-out');
            on(toast, 'transitionend', () => toast.remove());
        }, duration);
    }

    function addNotification(msg, type) {
        const div = document.createElement('div');
        div.className = `notification ${type}`;
        div.textContent = msg;
        els.raNotifications.append(div);
    }

    function registerEventListeners() {
        // Spec selector
        on(els.specSelector, 'change', async () => {
            const spec = els.specSelector.value;

            window.history.replaceState(
                null,
                '',
                `/specs/${spec}/#/`
            );

            await renderSpec(spec);
        });


        // Filter modal
        on(els.filterBtn, 'click', openFilterModal);
        on(els.filterClose, 'click', () => els.filterModal.classList.remove('open'));
        on(els.filterApply, 'click', applyFilters);

        // Search
        on(els.searchBar, 'input', debounce(() => doSearch(0), 300));
        on(els.searchBar, 'keydown', e => {
            if (e.key === 'Enter') e.preventDefault();
        });

        // Close dropdown when clicking outside
        on(document, 'click', e => {
            if (!$('#search-container').contains(e.target)) els.results.style.display = 'none';
        });

        // Search modal pagination
        on(els.closeSearchModal, 'click', () => els.searchModal.classList.remove('open'));
        on(els.prevPage, 'click', () => changePage(-1));
        on(els.nextPage, 'click', () => changePage(1));

        // RA Search
        on(els.raBtn, 'click', () => {
            els.raModal.classList.add('open');
            els.raStatus.textContent = '';
            els.raResponse.value = '';
            els.raInput.value = '';
            els.raNotifications.querySelectorAll('.notification').forEach(n => n.remove());
            els.raSubmit.textContent = 'Search';
            els.results.style.display = 'none';
        });

        on(els.raClose, 'click', () => els.raModal.classList.remove('open'));
        on(els.raInput, 'input', () => {
            els.raSubmit.textContent = 'Search';
        });
        on(els.raSubmit, 'click', handleRaSearch);
        on(els.raAction, 'click', raAction);

        on(window, 'hashchange', () => {
            const panel = $('.TryItPanel', els.viewer);
            if (panel) {
                panel.__eOpKey = undefined;
                attachTryIt(currentSpec);
            }
        });
    }

    registerEventListeners();
    loadSpecs().catch(err => console.error('Failed to initialise specs:', err));
})();
