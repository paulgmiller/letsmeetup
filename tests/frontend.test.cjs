const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');
const {webcrypto} = require('node:crypto');

class Element {
  constructor() { this.value = ''; this.textContent = ''; this.children = []; this.hidden = false; this.style = {}; }
  addEventListener(event, handler) { this[event] = handler; }
  setAttribute(name, value) { this[name] = value; }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  focus() { this.focused = true; }
  select() { this.selected = true; }
  scrollIntoView() {}
}
const tick = () => new Promise(setImmediate);
function app(options = {}) {
  const elements = Object.fromEntries([...readFileSync('static/index.html','utf8').matchAll(/id="([^"]+)"/g)].map(m => [m[1], new Element()]));
  elements['share-link'].hidden = true;
  const requests = [], searches = [], legacySearches = [], intervals = [], timers = [], locations = [];
  const backend = {guests: options.guests || [], query: 'food', expired: !!options.expired};
  const context = vm.createContext({URL, btoa, crypto: webcrypto, console: {error() {}}, setTimeout: (fn,ms) => {timers.push({fn,ms});return timers.length;}, clearTimeout: () => {},
    window: {location: {href: options.url || 'https://meetup.example/?m=friends'},
      history: {replaceState(_s, _t, href) { context.window.location.href = href; }}},
    document: {hidden: false, getElementById: id => elements[id], createElement: () => new Element(),
      addEventListener(event, handler) { this[event] = handler; }},
    navigator: {geolocation: {getCurrentPosition(success, failure) {
      locations.push({success,failure});
      if (options.locate) return options.locate(success, failure);
      if (locations.length === 1 && !options.autoJoin) return;
      options.locationError ? failure({code: options.locationCode || 1, message: 'Permission denied'}) : success({coords: {latitude: 47, longitude: -122}});
    }}, ...options.navigator},
    setInterval(fn, delay) { intervals.push({fn, delay}); },
    fetch: async (url, init = {}) => {
      requests.push({url, ...init});
      if (options.beforeFetch) await options.beforeFetch(url, init);
      if (backend.expired) return {ok: false, status: 410};
      if (options.offline || (options.saveError && init.method === 'PUT')) return {ok: false, status: 500};
      const search = url.startsWith('/meetup-search/');
      if (init.method === 'POST') {
        const guest = {...JSON.parse(init.body), GuestId: 'own'};
        backend.guests = backend.guests.filter(g => g.GuestId !== 'own').concat(guest);
      }
      if (init.method === 'DELETE') backend.guests = backend.guests.filter(g => g.GuestId !== 'own');
      if (init.method === 'PUT') backend.query = JSON.parse(init.body).query;
      // Capture data now, rather than sharing mutable backend arrays with the page.
      const data = JSON.parse(JSON.stringify(search ? {query: backend.query} : backend.guests));
      return {ok: true, status: init.method === 'DELETE' ? 204 : 200,
        headers: {get: name => name === 'X-Guest-ID' ? 'own' : name === 'X-Meetup-Expires' ? '2030-01-01T12:00:00Z' : null},
        json: async () => {if (options.beforeJSON) await options.beforeJSON(url,init);return data;}};
    },
    google: {maps: {
      importLibrary: async () => ({}),
      Map: class { constructor(_element,config) { this.center=config.center;this.zoom=config.zoom;this.fits=0; } addListener(event, fn) { this[event] = fn; } fitBounds() {this.fits++;} setZoom(zoom) {this.zoom=zoom;} setCenter(center) {this.center=center;} },
      LatLngBounds: class { extend() {} getCenter() { return {lat: 47, lng: -122}; } },
      marker: {PinElement: class { constructor() { this.element = new Element(); } },
        AdvancedMarkerElement: class { constructor(config) { Object.assign(this, config); } append() {} }},
      places: {RankBy: {DISTANCE:'distance'}, PlacesService: class {nearbySearch(request, callback) {legacySearches.push({request,callback});}}, SearchByTextRankPreference: {DISTANCE: 'distance'}, Place: {searchByText(request) {
        return new Promise((resolve, reject) => searches.push({request, resolve, reject}));
      }}}
    }}
  });
  vm.runInContext(readFileSync('static/main.js', 'utf8'), context);
  return {context, elements, requests, searches, legacySearches, intervals, timers, locations, backend,
    async ready() { await tick(); await context.initMap(); await tick(); },
    async join() { await elements['location-button'].click(); await tick(); },
    async search(query) { elements['search-query'].value = query; return elements['search-form'].submit({preventDefault() {}}); },
    async share() { await elements['share-button'].click(); },
    async refresh() { await context.refreshMeetup(); await tick(); }
  };
}
const result = name => ({places: [{id: 'place-id', displayName: name, formattedAddress: '123 Main St', location: {lat: 47, lng: -122}}]});

test('a pending location permission leaves the meetup visible without sharing yet', async () => {
  const a = app(); await a.ready();
  assert.equal(a.requests.filter(r => r.method === 'POST').length, 0);
  assert.equal(a.context.appState.guests.length, 0);
  assert.equal(a.searches.length, 0);
  assert.equal(a.elements['search-query'].value, 'food');
  assert.equal(a.intervals[0].delay, 12000);
});

test('new links use compact random tokens and existing invite links survive', async () => {
  const a = app({url: 'https://meetup.example/'});
  const token=new URL(a.context.window.location.href).searchParams.get('m');
  assert.match(token, /^[A-Za-z0-9_-]{22}$/);
  assert.equal(Buffer.from(token,'base64url').length,16);
  const another=app({url: 'https://meetup.example/'});
  assert.notEqual(new URL(another.context.window.location.href).searchParams.get('m'),token);
  const b = app({url: 'https://meetup.example/?meetup=legacy'}); await b.ready();
  assert.equal(b.requests[0].url, '/meetups/legacy');
});

test('explicit join shares a name and snapshot, renders places and directions', async () => {
  const a = app(); await a.ready(); a.elements['guest-name'].value = 'Sam'; await a.join();
  const body = JSON.parse(a.requests.find(r => r.method === 'POST').body);
  assert.deepEqual(body, {GuestName: 'Sam', Lat: 47, Long: -122});
  assert.equal(a.elements['guests'].children[0].textContent, 'Sam (you)');
  assert.equal(a.elements['leave-button'].hidden, false);
  a.searches[0].resolve(result('Coffee')); await tick();
  assert.equal(a.elements['places'].children.length, 1);
  const directions = a.elements['places'].children[0].children[1];
  assert.match(directions.href, /destination_place_id=place-id/);
  assert.equal(directions.target, '_blank');
});

test('polling picks up friends and shared searches without reposting location', async () => {
  const a = app(); await a.ready(); await a.join();
  a.backend.guests.push({GuestId: 'friend', GuestName: '<script>friend</script>', Lat: 48, Long: -123});
  a.backend.query = 'tacos'; await a.refresh();
  assert.equal(a.elements['guest-count'].textContent, '2');
  assert.equal(a.elements['guests'].children[1].textContent, '<script>friend</script>');
  assert.equal(a.searches.at(-1).request.textQuery, 'tacos');
  assert.equal(a.requests.filter(r => r.method === 'POST').length, 1);
});

test('polls pause while hidden, and unchanged state avoids repeated place searches', async () => {
  const a = app(); await a.ready(); await a.join();
  const count = a.requests.length; a.context.document.hidden = true; await a.refresh();
  assert.equal(a.requests.length, count);
  a.context.document.hidden = false; await a.refresh(); assert.equal(a.searches.length, 1);
});

test('editing a search survives a background refresh', async () => {
  const a = app(); await a.ready();
  a.context.document.activeElement = a.elements['search-query'];
  a.elements['search-query'].value = 'typing'; a.backend.query = 'coffee'; await a.refresh();
  assert.equal(a.elements['search-query'].value, 'typing');
  assert.equal(a.context.appState.query, 'coffee');
});

test('search saves in place and ignores older place responses', async () => {
  const a = app({url: 'https://meetup.example/?m=friends&query=old'}); await a.ready(); await a.join();
  const searching = a.search(' coffee '); await tick();
  assert.equal(a.searches[1].request.textQuery, 'coffee');
  a.searches[1].resolve(result('Coffee')); await searching;
  a.searches[0].resolve(result('Stale food')); await tick();
  assert.equal(a.elements['places'].children[0].children[0].children[0].textContent, 'Coffee');
  assert.equal(a.context.window.location.href, 'https://meetup.example/?m=friends');
});

test('manual pin selection joins without geolocation', async () => {
  const a = app({locationError: true}); await a.ready(); await a.join();
  assert.match(a.elements['location-status'].textContent, /choose a spot/i);
  a.elements['pin-button'].click();
  a.context.appState.map.click({latLng: {lat: () => 12, lng: () => 34}}); await tick();
  assert.equal(a.backend.guests[0].Lat, 12);
  assert.equal(a.context.appState.choosingPin, false);
});

test('leaving removes own snapshot without affecting friends', async () => {
  const a = app(); await a.ready(); await a.join();
  a.backend.guests.push({GuestId: 'friend', Lat: 48, Long: -123}); await a.refresh();
  await a.elements['leave-button'].click();
  assert.equal(a.backend.guests.length, 1);
  assert.equal(a.elements['leave-button'].hidden, true);
  assert.match(a.elements['location-status'].textContent, /removed/);
});

test('expired links clear locations and disable sharing and edits', async () => {
  const a = app(); await a.ready(); await a.join(); a.backend.expired = true; await a.refresh();
  assert.equal(a.context.appState.expired, true);
  assert.equal(a.elements['guests'].children.length, 0);
  assert.equal(a.elements['share-button'].disabled, true);
  assert.equal(a.elements['location-button'].disabled, true);
  assert.match(a.elements['meetup-status'].textContent, /expired/);
  const count = a.requests.length; await a.refresh(); assert.equal(a.requests.length, count);
  a.searches[0].resolve(result('Late coffee')); await tick(); assert.equal(a.elements['places'].children.length, 0);
});

test('failed requests keep retry available', async () => {
  const a = app({saveError: true}); await a.ready(); await a.search('coffee');
  assert.match(a.elements['search-status'].textContent, /Could not save/);
  assert.equal(a.elements['search-button'].disabled, false);
  a.context.mapLoadFailed(); assert.match(a.elements['search-status'].textContent, /map could not load/);
});

test('native share, clipboard fallback, and selectable link remain supported', async () => {
  let shared, copied;
  const a = app({navigator: {share: async data => {shared = data;}}}); await a.share();
  assert.equal(shared.url, a.context.window.location.href);
  const b = app({navigator: {share: async () => {throw new Error('Unavailable');}, clipboard: {writeText: async url => {copied = url;}}}}); await b.share();
  assert.equal(copied, b.context.window.location.href);
  const c = app(); await c.share(); assert.equal(c.elements['share-link'].selected, true);
  const d = app({navigator: {share: async () => {throw {name: 'AbortError'};}}}); await d.share();
  assert.equal(d.elements['share-status'].textContent, 'Sharing canceled.'); assert.equal(d.elements['share-link'].hidden, true);
});


test('an open page expires locally without waiting for another successful poll', async () => {
 const a=app();await a.ready();await a.join();
 assert.ok(a.timers.at(-1).ms>0);
 a.timers.at(-1).fn();
 assert.equal(a.context.appState.expired,true);
 assert.equal(a.elements['places'].children.length,0);
 assert.equal(a.elements['location-button'].disabled,true);
});

test('a delayed poll cannot replace a newly shared snapshot',async()=>{
 let hold=false,release;
 const a=app({beforeJSON:async(url,init)=>{
  if(hold && !init.method && url.startsWith('/meetups/')) await new Promise(resolve=>{release=resolve;});
 }});
 await a.ready();hold=true;
 const poll=a.refresh();await tick();
 await a.join();
 assert.equal(a.context.appState.guests.length,1);
 hold=false;release();await poll;
 assert.equal(a.context.appState.guests.length,1);
});



test('startup shares the granted location and renders a marker without another click',async()=>{
 const a=app({autoJoin:true});await a.ready();
 assert.equal(a.requests.filter(r=>r.method==='POST').length,1);
 assert.equal(a.context.appState.guests.length,1);
 assert.equal(a.context.appState.markers.length,1);
 assert.equal(a.context.appState.markers[0].title,'Friend 1');
 assert.equal(JSON.stringify(a.context.appState.markers[0].position),JSON.stringify({lat:47,lng:-122}));
 assert.equal(a.context.appState.map.zoom,14);
 assert.equal(a.searches.length,1);
});

test('startup updates an existing participant without losing their name',async()=>{
 const a=app({autoJoin:true,guests:[{GuestId:'own',GuestName:'Sam',Lat:10,Long:20},{GuestId:'friend',Lat:11,Long:21}]});
 await a.ready();
 const own=a.backend.guests.find(g=>g.GuestId==='own');
 assert.equal(own.GuestName,'Sam');assert.equal(own.Lat,47);
 assert.equal(a.backend.guests.length,2);
});

test('denied startup location leaves map pin selection available',async()=>{
 const a=app({autoJoin:true,locationError:true});await a.ready();
 assert.match(a.elements['location-status'].textContent,/choose a spot/i);
 assert.equal(a.requests.filter(r=>r.method==='POST').length,0);
 a.elements['pin-button'].click();assert.equal(a.context.appState.choosingPin,true);
});

test('a delayed startup location cannot undo a manual pin, a leave, or expiry',async()=>{
 for(const interaction of ['pin','leave','expired']) {
  let locate;
  const a=app({locate:success=>{locate=success;}});await a.ready();
  if(interaction==='pin') {
   a.elements['pin-button'].click();
   a.context.appState.map.click({latLng:{lat:()=>12,lng:()=>34}});await tick();
  }
  if(interaction==='leave') await a.elements['leave-button'].click();
  if(interaction==='expired') a.context.expireMeetup();
  const posts=a.requests.filter(r=>r.method==='POST').length;
  locate({coords:{latitude:47,longitude:-122}});await tick();
  assert.equal(a.requests.filter(r=>r.method==='POST').length,posts,interaction);
 }
});

test('disabled Places API New falls back to the working original API',async()=>{
 const a=app();await a.ready();await a.join();
 const error=new Error('Places API (New) has not been used in project 123 before or it is disabled.');error.code='PERMISSION_DENIED';
 a.searches[0].reject(error);await tick();
 assert.equal(a.legacySearches.length,1);
 assert.equal(a.legacySearches[0].request.keyword,'food');
 a.legacySearches[0].callback([{place_id:'legacy-id',name:'Coffee',vicinity:'123 Main St',geometry:{location:{lat:47,lng:-122}}}],'OK');await tick();
 assert.equal(a.elements['places'].children.length,1);
 assert.match(a.elements['places'].children[0].children[1].href,/legacy-id/);
 const searching=a.search('tea');await tick();
 assert.equal(a.searches.length,1,'do not repeatedly call the disabled API');
 assert.equal(a.legacySearches[1].request.keyword,'tea');
 a.legacySearches[1].callback([],'ZERO_RESULTS');await searching;
 assert.match(a.elements['search-status'].textContent,/No places found/);
});

test('permission and quota failures are specific and do not trigger a fallback',async()=>{
 for(const [code,message] of [['PERMISSION_DENIED',/not enabled/],['RESOURCE_EXHAUSTED',/limit/]]) {
  const a=app();await a.ready();await a.join();
  const error=new Error('Search failed');error.code=code;
  a.searches[0].reject(error);await tick();
  assert.equal(a.legacySearches.length,0);
  assert.match(a.elements['search-status'].textContent,message);
 }
});


test('opening an expired meetup never shares the granted location',async()=>{
 const a=app({autoJoin:true,expired:true});await a.ready();
 assert.equal(a.context.appState.expired,true);
 assert.equal(a.locations.length,0);
 assert.equal(a.requests.filter(r=>r.method==='POST').length,0);
});


test('a slow browser location keeps the placeholder visible instead of a default map',async()=>{
 let locate;
 const a=app({locate:success=>{locate=success;}});await a.ready();
 assert.equal(a.elements['map-loading'].hidden,false);
 assert.notEqual(a.elements.map.style.visibility,'visible');
 assert.equal(a.elements['location-status'].textContent,'Finding your location…');
 locate({coords:{latitude:47,longitude:-122}});await tick();
 assert.equal(a.elements['map-loading'].hidden,true);
 assert.equal(a.elements.map.style.visibility,'visible');
 assert.equal(a.context.appState.map.center.lat,47);
});

test('the map centers before the server finishes saving the location',async()=>{
 let release;
 const a=app({autoJoin:true,beforeFetch:async(_url,init)=>{
  if(init.method==='POST') await new Promise(resolve=>{release=resolve;});
 }});await a.ready();
 assert.equal(a.context.appState.guests.length,0);
 assert.equal(a.context.appState.map.center.lat,47);
 assert.equal(a.context.appState.map.zoom,14);
 assert.equal(a.elements.map.style.visibility,'visible');
 release();await tick();assert.equal(a.context.appState.markers.length,1);
});

test('location denial and timeout explain the cause and keep manual selection usable',async()=>{
 for(const [code,message] of [[1,/blocked/],[2,/determine/],[3,/in time/]]) {
  const a=app({autoJoin:true,locationError:true,locationCode:code});await a.ready();
  assert.match(a.elements['location-status'].textContent,message);
  assert.equal(a.elements['map-loading'].hidden,false);
  assert.equal(a.requests.filter(r=>r.method==='POST').length,0);
  a.elements['pin-button'].click();
  assert.equal(a.elements['map-loading'].hidden,true);
  assert.equal(a.elements.map.style.visibility,'visible');
 }
});
