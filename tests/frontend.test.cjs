const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');

function app(options = {}) {
  const elements = {};
  for (const id of ['search-query', 'search-button', 'search-status', 'search-form',
    'share-button', 'share-status', 'share-link', 'map']) {
    elements[id] = {value: '', textContent: '', hidden: id === 'share-link',
      addEventListener(event, handler) { this[event] = handler; },
      focus() { this.focused = true; }, select() { this.selected = true; }};
  }
  const requests = [], searches = [], markers = [];
  const context = vm.createContext({
    URL, URLSearchParams, btoa, console,
    window: {location: {href: 'https://meetup.example/?m=friends&query=old'},
      history: {replaceState(_state, _title, href) { context.window.location.href = href; }}},
    document: {cookie: 'GuestId=existing-guest', getElementById: id => elements[id],
      createElement: () => ({textContent: ''})},
    navigator: {
      geolocation: {getCurrentPosition(success, failure) {
        return options.locationError ? failure({message: 'Permission denied'}) :
          success({coords: {latitude: 47, longitude: -122}});
      }},
      ...options.navigator
    },
    fetch: async (url, init = {}) => {
      requests.push({url, ...init});
      if (init.method === 'POST') return {ok: true, json: async () => [{Lat: 47, Long: -122}]};
      return {ok: init.method === 'PUT' ? !options.saveError : !options.loadError,
        json: async () => ({query: 'food'})};
    },
    google: {maps: {
      Map: class { fitBounds() {} },
      LatLngBounds: class { extend() {} getCenter() { return {lat: 47, lng: -122}; } },
      Marker: class {
        constructor(config) { this.config = config; markers.push(this); }
        addListener(event, callback) { this[event] = callback; }
        setMap(map) { this.config.map = map; }
      },
      InfoWindow: class { open() {} close() {} }, Size: class {}, Animation: {DROP: 'drop'},
      places: {RankBy: {DISTANCE: 'distance'}, PlacesService: class {
        nearbySearch(request, callback) { searches.push({request, callback}); }
      }}
    }}
  });
  vm.runInContext(readFileSync('static/main.js', 'utf8'), context);
  return {context, elements, requests, searches, markers,
    async ready() { await context.initMap(); await new Promise(setImmediate); },
    async search(query) {
      elements['search-query'].value = query;
      await elements['search-form'].submit({preventDefault() {}});
    },
    async share() { await elements['share-button'].click(); }
  };
}

const result = name => [{name, icon: 'icon.png', geometry: {location: {lat: 47, lng: -122}}}];

test('search saves the meetup query and refreshes places without navigation or another location request', async () => {
  const a = app();
  await a.ready();
  a.searches[0].callback(result('Food'), 'OK');
  const oldMarker = a.markers[1];
  await a.search('  coffee  ');
  assert.equal(JSON.parse(a.requests.at(-1).body).query, 'coffee');
  assert.equal(a.requests.at(-1).url, '/meetup-search/friends');
  assert.equal(a.searches[1].request.keyword, 'coffee');
  a.searches[1].callback(result('Coffee'), 'OK');
  assert.equal(oldMarker.config.map, null);
  assert.match(a.elements['search-status'].textContent, /Showing 1.*coffee/);
  assert.equal(a.elements['search-button'].disabled, false);
  assert.equal(a.context.window.location.href, 'https://meetup.example/?m=friends');
  assert.equal(a.requests.filter(r => r.method === 'POST').length, 1);
  assert.equal(JSON.parse(a.requests.find(r => r.method === 'POST').body).GuestId, 'existing-guest');
});

test('late places responses cannot replace a newer search', async () => {
  const a = app();
  await a.ready();
  await a.search('coffee');
  a.searches[1].callback(result('Coffee'), 'OK');
  a.searches[0].callback(result('Stale food'), 'OK');
  assert.match(a.elements['search-status'].textContent, /coffee/);
  assert.equal(a.context.placeMarkers[0].config.title, 'Coffee');
});

test('a successful save recovers from a failed initial search lookup', async () => {
  const a = app({loadError: true});
  await a.ready();
  assert.match(a.elements['search-status'].textContent, /Could not load the shared search/);
  assert.equal(a.searches.length, 0);
  await a.search('coffee');
  await new Promise(setImmediate);
  assert.equal(a.searches[0].request.keyword, 'coffee');
});

test('empty results and places failures have visible feedback', async () => {
  const a = app();
  await a.ready();
  a.searches[0].callback([], 'ZERO_RESULTS');
  assert.match(a.elements['search-status'].textContent, /No nearby places/);
  await a.search('tea');
  a.searches[1].callback(null, 'REQUEST_DENIED');
  assert.match(a.elements['search-status'].textContent, /REQUEST_DENIED/);
});

test('failed saves leave search enabled for retry', async () => {
  const a = app({saveError: true});
  await a.ready();
  await a.search('coffee');
  assert.match(a.elements['search-status'].textContent, /Could not save/);
  assert.equal(a.elements['search-button'].disabled, false);
  assert.equal(a.searches.length, 1);
});

test('location and map failures are visible and sharing remains available', async () => {
  const a = app({locationError: true});
  await a.ready();
  assert.match(a.elements['search-status'].textContent, /Permission denied/);
  await a.search('coffee');
  assert.match(a.elements['search-status'].textContent, /Permission denied/);
  a.context.mapLoadFailed();
  assert.match(a.elements['search-status'].textContent, /map could not load/);
  await a.share();
  assert.equal(a.elements['share-link'].hidden, false);
});

test('share uses native sharing when available', async () => {
  let shared;
  const a = app({navigator: {share: async data => { shared = data; }}});
  await a.share();
  assert.equal(shared.url, a.context.window.location.href);
  assert.equal(a.elements['share-status'].textContent, 'Link shared!');
});

test('share falls back to the clipboard after native sharing fails', async () => {
  let copied;
  const a = app({navigator: {
    share: async () => { throw new Error('Unavailable'); },
    clipboard: {writeText: async url => { copied = url; }}
  }});
  await a.share();
  assert.equal(copied, a.context.window.location.href);
  assert.equal(a.elements['share-status'].textContent, 'Link copied!');
});

test('share shows a selected link when clipboard access is unavailable', async () => {
  const a = app();
  await a.share();
  const link = a.elements['share-link'];
  assert.equal(link.hidden, false);
  assert.equal(link.value, a.context.window.location.href);
  assert.equal(link.selected, true);
  assert.equal(link.focused, true);
});

test('canceling native sharing reports cancellation without copying', async () => {
  const a = app({navigator: {share: async () => { throw {name: 'AbortError'}; }}});
  await a.share();
  assert.equal(a.elements['share-status'].textContent, 'Sharing canceled.');
  assert.equal(a.elements['share-link'].hidden, true);
});
