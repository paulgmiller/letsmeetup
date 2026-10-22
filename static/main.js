/* A single page, a shared link, and location snapshots. No background tracking. */
var $ = id => document.getElementById(id);
var meetupUrl = new URL(window.location.href);
if (!meetupUrl.searchParams.get('meetup') && !meetupUrl.searchParams.get('m')) {
  var inviteBytes = crypto.getRandomValues(new Uint8Array(16));
  var invite = btoa(String.fromCharCode(...inviteBytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  meetupUrl.searchParams.set('m', invite);
  window.history.replaceState(null, '', meetupUrl.href);
}
var meetupId = meetupUrl.searchParams.get('meetup') || meetupUrl.searchParams.get('m');
var endpoint = '/meetups/' + encodeURIComponent(meetupId);
var searchEndpoint = '/meetup-search/' + encodeURIComponent(meetupId);
var appState = {guests: [], ownID: '', query: '', map: null, markers: [], placeMarkers: [],
  choosingPin: false, expired: false, refreshing: false, revision: 0, generation: 0, busy: false, mapError: false, savingSearch: false, expiryTimer: null, initialViewMoved: false};

function expireMeetup() {
  appState.expired = true;
  appState.generation++;
  appState.guests = [];
  appState.markers.concat(appState.placeMarkers).forEach(marker => { marker.map = null; });
  appState.markers = []; appState.placeMarkers = [];
  $('guests').replaceChildren(); $('places').replaceChildren(); $('guest-count').textContent = '0';
  $('meetup-status').textContent = 'This meetup has expired. Start a new meetup to make a fresh plan.';
  $('location-status').textContent = 'Location sharing has ended.';
  $('search-status').textContent = '';
  ['location-button', 'pin-button', 'search-button', 'search-query', 'guest-name', 'share-button', 'leave-button'].forEach(id => { $(id).disabled = true; });
}

async function request(url, options) {
  var response = await fetch(url, {cache: 'no-store', ...options});
  if (response.status === 410) { expireMeetup(); throw new Error('expired'); }
  if (!response.ok) throw new Error('request failed');
  var expiry = response.headers.get('X-Meetup-Expires');
  if (expiry) {
    clearTimeout(appState.expiryTimer);
    var remaining = new Date(expiry).getTime() - Date.now();
    if (remaining <= 0) { expireMeetup(); throw new Error('expired'); }
    appState.expiryTimer = setTimeout(expireMeetup, remaining);
    $('expiry').textContent = 'Meetup ends ' + new Date(expiry).toLocaleString([], {weekday: 'short', hour: 'numeric', minute: '2-digit'}) + '. Saved locations are deleted within an hour after it ends.';
  }
  return response;
}

async function refreshMeetup() {
  if (appState.expired || appState.refreshing || appState.busy || appState.savingSearch || document.hidden) return;
  appState.refreshing = true;
  var revision = appState.revision;
  try {
    var responses = await Promise.all([request(endpoint), request(searchEndpoint)]);
    var guests = await responses[0].json(), search = await responses[1].json();
    if (revision !== appState.revision || appState.expired) return;
    appState.ownID = responses[0].headers.get('X-Guest-ID') || '';
    applyMeetup(guests, search.query);
    $('meetup-status').textContent = 'Invite a friend. Find your middle ground.';
  } catch (error) {
    if (!appState.expired) $('meetup-status').textContent = 'Could not refresh the meetup. We’ll try again shortly.';
  } finally { appState.refreshing = false; }
}

function applyMeetup(guests, query) {
  var changed = JSON.stringify(guests) !== JSON.stringify(appState.guests) || query !== appState.query;
  appState.guests = guests;
  appState.query = query;
  if (document.activeElement !== $('search-query')) $('search-query').value = query;
  $('search-button').disabled = appState.expired;
  renderGuests();
  if (changed && appState.map) { renderGuestMarkers(); searchNearby(); }
}

function renderGuests() {
  var own = appState.guests.find(g => g.GuestId === appState.ownID);
  $('guest-count').textContent = String(appState.guests.length);
  $('leave-button').hidden = !own;
  $('location-button').textContent = own ? 'Update my location' : 'Use my location';
  if (own && document.activeElement !== $('guest-name')) $('guest-name').value = own.GuestName;
  var items = appState.guests.map((guest, index) => {
    var item = document.createElement('li');
    item.textContent = (guest.GuestName || 'Friend ' + (index + 1)) + (guest.GuestId === appState.ownID ? ' (you)' : '');
    return item;
  });
  if (!items.length) { var empty = document.createElement('li'); empty.textContent = 'Be the first to join.'; items.push(empty); }
  $('guests').replaceChildren(...items);
}

function mapLoadFailed() {
  if (appState.expired) return;
  appState.mapError = true;
  $('search-status').textContent = 'The map could not load. Reload to try again. You can still share this meetup.';
  $('pin-button').disabled = true;
}
window.gm_authFailure = mapLoadFailed;

async function initMap() {
  if (appState.map) return;
  try {
    await Promise.all(['maps', 'marker', 'places'].map(name => google.maps.importLibrary(name)));
    appState.map = new google.maps.Map($('map'), {center: {lat: 39, lng: -98}, zoom: 3,
      mapId: 'DEMO_MAP_ID', mapTypeControl: false, streetViewControl: false, fullscreenControl: false});
    appState.map.addListener('click', event => {
      if (appState.choosingPin && !appState.busy && !appState.expired) {
        saveLocation(event.latLng.lat(), event.latLng.lng());
      }
    });
    // Center locally without joining or publishing coordinates to the meetup.
    if (!appState.guests.length && !appState.expired) {
      ['pointerdown', 'wheel'].forEach(event => $('map').addEventListener(event, () => { appState.initialViewMoved = true; }));
      if (navigator.geolocation) {
        var revision = appState.revision;
        navigator.geolocation.getCurrentPosition(position => {
          if (appState.expired || appState.guests.length || appState.choosingPin || appState.initialViewMoved || appState.revision !== revision) return;
          appState.map.setCenter({lat: position.coords.latitude, lng: position.coords.longitude});
          appState.map.setZoom(14);
        }, () => {
          if (!appState.expired && !appState.guests.length && !appState.busy && !appState.choosingPin) {
            $('location-status').textContent = 'Allow location access to start near you, or choose a spot on the map.';
          }
        }, {timeout: 15000, maximumAge: 60000});
      }
    }
    renderGuestMarkers();
    searchNearby();
  } catch (error) { mapLoadFailed(); }
}

function renderGuestMarkers() {
  appState.markers.forEach(marker => { marker.map = null; });
  appState.markers = appState.guests.map((guest, index) => {
    var label = guest.GuestId === appState.ownID ? 'You' : String(index + 1);
    var pin = new google.maps.marker.PinElement({glyphText: label, background: '#245b45', borderColor: '#fff', glyphColor: '#fff'});
    var marker = new google.maps.marker.AdvancedMarkerElement({map: appState.map,
      position: {lat: guest.Lat, lng: guest.Long}, title: guest.GuestName || 'Friend ' + (index + 1)});
    marker.append(pin);
    return marker;
  });
}

function guestBounds() {
  var bounds = new google.maps.LatLngBounds();
  appState.guests.forEach(g => bounds.extend({lat: g.Lat, lng: g.Long}));
  return bounds;
}

async function searchNearby() {
  var generation = ++appState.generation;
  appState.placeMarkers.forEach(marker => { marker.map = null; });
  appState.placeMarkers = [];
  $('places').replaceChildren();
  if (appState.expired) return;
  if (!appState.map) {
    $('search-status').textContent = appState.mapError ? 'The map could not load. Reload to try again.' : 'Search saved. Waiting for the map…';
    return;
  }
  if (!appState.guests.length) { $('search-status').textContent = 'Add a location to find places near the middle.'; return; }
  var bounds = guestBounds();
  appState.map.fitBounds(bounds, 60);
  if (appState.guests.length === 1) appState.map.setZoom(14);
  if (!appState.query) return;
  var center = bounds.getCenter();
  $('search-status').textContent = 'Finding “' + appState.query + '” near the middle…';
  try {
    // Text search preserves free-form queries such as “tea & cake”.
    var response = await google.maps.places.Place.searchByText({textQuery: appState.query,
      fields: ['id', 'displayName', 'formattedAddress', 'location'],
      locationBias: {center: center, radius: 5000}, rankPreference: google.maps.places.SearchByTextRankPreference.DISTANCE,
      maxResultCount: 3});
    if (generation !== appState.generation || appState.expired) return;
    var places = (response.places || []).filter(place => place.location);
    places.slice(0, 3).forEach((place, index) => {
      var pin = new google.maps.marker.PinElement({glyphText: String(index + 1), background: '#e8bf7c', borderColor: '#fff', glyphColor: '#203b33'});
      var marker = new google.maps.marker.AdvancedMarkerElement({map: appState.map,
        position: place.location, title: place.displayName});
      marker.append(pin);
      appState.placeMarkers.push(marker);
      bounds.extend(place.location);
      var item = document.createElement('li'), detail = document.createElement('div');
      detail.className = 'place-detail';
      var title = document.createElement('strong'), address = document.createElement('span');
      title.textContent = place.displayName; address.textContent = place.formattedAddress;
      detail.append(title, address);
      var link = document.createElement('a');
      var destination = new URL('https://www.google.com/maps/dir/');
      destination.searchParams.set('api', '1'); destination.searchParams.set('destination', place.formattedAddress || place.displayName);
      destination.searchParams.set('destination_place_id', place.id);
      link.href = destination.href; link.target = '_blank'; link.rel = 'noopener noreferrer'; link.textContent = 'Directions ↗';
      link.setAttribute('aria-label', 'Directions to ' + place.displayName);
      item.append(detail, link); $('places').append(item);
    });
    if (places.length) appState.map.fitBounds(bounds, 60);
    $('search-status').textContent = places.length ? 'Places near the center of your shared locations.' : 'No places found. Try another search.';
  } catch (error) {
    if (generation === appState.generation && !appState.expired) $('search-status').textContent = 'Could not find places. Try searching again.';
  }
}

function setBusy(busy) {
  appState.busy = busy;
  ['location-button', 'pin-button', 'leave-button', 'guest-name'].forEach(id => { $(id).disabled = busy || appState.expired; });
}

async function saveLocation(lat, lng) {
  if (appState.expired) return;
  setBusy(true);
  appState.revision++;
  $('location-status').textContent = 'Sharing your location…';
  try {
    var response = await request(endpoint, {method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({GuestName: $('guest-name').value.trim(), Lat: lat, Long: lng})});
    appState.ownID = response.headers.get('X-Guest-ID') || '';
    var guests = await response.json();
    if (appState.expired) return;
    applyMeetup(guests, appState.query);
    appState.choosingPin = false; $('pin-button').setAttribute('aria-pressed', 'false');
    $('location-status').textContent = 'Location shared. Update it whenever you need to.';
  } catch (error) {
    if (!appState.expired) $('location-status').textContent = 'Could not share your location. Please try again.';
  } finally { appState.revision++; setBusy(false); }
}

$('location-button').addEventListener('click', () => {
  if (!navigator.geolocation) { $('location-status').textContent = 'Location is unavailable. Choose a spot on the map instead.'; return; }
  setBusy(true);
  $('location-status').textContent = 'Waiting for location permission…';
  navigator.geolocation.getCurrentPosition(position => {
    saveLocation(position.coords.latitude, position.coords.longitude);
  }, () => {
    setBusy(false);
    if (!appState.expired) $('location-status').textContent = 'Could not get your location. Allow access and try again, or choose a spot on the map.';
  }, {timeout: 15000, maximumAge: 60000});
});

$('pin-button').addEventListener('click', () => {
  if (!appState.map) { $('location-status').textContent = 'Wait for the map to load, then try again.'; return; }
  appState.choosingPin = !appState.choosingPin;
  $('pin-button').setAttribute('aria-pressed', String(appState.choosingPin));
  $('location-status').textContent = appState.choosingPin ? 'Tap a spot on the map to share that location. Tap “Choose on map” again to cancel.' : 'Pin selection canceled.';
  if (appState.choosingPin) $('map').scrollIntoView({block: 'center'});
});

$('guest-name').addEventListener('change', () => {
  var own = appState.guests.find(g => g.GuestId === appState.ownID);
  if (own && !appState.busy) saveLocation(own.Lat, own.Long);
});

$('leave-button').addEventListener('click', async () => {
  setBusy(true); appState.revision++;
  try {
    await request(endpoint, {method: 'DELETE'});
    applyMeetup(appState.guests.filter(g => g.GuestId !== appState.ownID), appState.query);
    $('location-status').textContent = 'You’ve left. Your shared location has been removed.';
  } catch (error) { if (!appState.expired) $('location-status').textContent = 'Could not leave. Please try again.'; }
  finally { appState.revision++; setBusy(false); }
});

$('search-form').addEventListener('submit', async event => {
  event.preventDefault();
  var query = $('search-query').value.trim();
  if (!query || appState.expired || appState.savingSearch) return;
  appState.savingSearch = true;
  appState.revision++;
  $('search-button').disabled = true;
  $('search-status').textContent = 'Saving shared search…';
  try {
    await request(searchEndpoint, {method: 'PUT', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({query})});
    if (appState.expired) return;
    appState.query = query;
    var url = new URL(window.location.href); url.searchParams.delete('query'); window.history.replaceState(null, '', url.href);
    await searchNearby();
  } catch (error) { if (!appState.expired) $('search-status').textContent = 'Could not save the search. Please try again.'; }
  finally { appState.savingSearch = false; appState.revision++; $('search-button').disabled = appState.expired; }
});

$('share-button').addEventListener('click', async () => {
  var status = $('share-status'), url = window.location.href;
  if (navigator.share) {
    try { await navigator.share({title: 'Let’s meet up', url}); status.textContent = 'Link shared!'; return; }
    catch (error) { if (error.name === 'AbortError') { status.textContent = 'Sharing canceled.'; return; } }
  }
  try { await navigator.clipboard.writeText(url); status.textContent = 'Link copied!'; }
  catch (error) { var link = $('share-link'); link.value = url; link.hidden = false; link.focus(); link.select(); status.textContent = 'Copy this link and send it to a friend.'; }
});

refreshMeetup();
setInterval(refreshMeetup, 12000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) refreshMeetup(); });
