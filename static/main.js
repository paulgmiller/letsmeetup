var map;
var guestPositions = [];
var placeMarkers = [];
var placeInfoWindows = [];
var searchGeneration = 0;
var mapError = '';
var mapInitializing = false;

// Prepare the meetup link even before the map or geolocation is available.
var meetupUrl = new URL(window.location.href);
if (!meetupUrl.searchParams.has('meetup') && !meetupUrl.searchParams.has('m')) {
  meetupUrl.searchParams.set('m', btoa(Math.floor(Math.random() * 1000000)));
  window.history.replaceState(null, '', meetupUrl.href);
}

var searchInput = document.getElementById('search-query');
var searchButton = document.getElementById('search-button');
var searchStatus = document.getElementById('search-status');
var meetupId = meetupUrl.searchParams.get('meetup') || meetupUrl.searchParams.get('m');
var searchEndpoint = '/meetup-search/' + encodeURIComponent(meetupId);
var savedQuery;
searchStatus.textContent = 'Loading shared search…';
searchInput.disabled = true;
var searchReady = fetch(searchEndpoint, {cache: 'no-store'})
  .then(function(response) {
    if (!response.ok) throw new Error('Could not load search');
    return response.json();
  })
  .then(function(search) {
    savedQuery = search.query;
    searchInput.value = savedQuery;
    return true;
  })
  .catch(function() {
    searchStatus.textContent = 'Could not load the shared search. Reload to try again.';
    return false;
  })
  .finally(function() {
    searchInput.disabled = false;
    searchButton.disabled = false;
  });

document.getElementById('search-form').addEventListener('submit', async function(event) {
  event.preventDefault();
  var query = searchInput.value.trim();
  if (!query) {
    searchInput.focus();
    return;
  }
  searchButton.disabled = true;
  searchStatus.textContent = 'Saving…';
  try {
    var response = await fetch(searchEndpoint, {
      method: 'PUT',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({query: query})
    });
    if (!response.ok) throw new Error('Could not save search');
    savedQuery = query;
    var url = new URL(window.location.href);
    url.searchParams.delete('query');
    window.history.replaceState(null, '', url.href);
    if (map) searchNearby();
    else {
      searchStatus.textContent = mapError || 'Search saved. Waiting for the map and your location…';
      if (!mapInitializing && !mapError && typeof google !== 'undefined' && google.maps) initMap();
    }
  } catch (error) {
    searchStatus.textContent = 'Could not save the search. Please try again.';
  } finally {
    searchButton.disabled = false;
  }
});

document.getElementById('share-button').addEventListener('click', async function() {
  var status = document.getElementById('share-status');
  var url = window.location.href;
  status.textContent = 'Sharing…';
  if (navigator.share) {
    try {
      await navigator.share({title: 'Lets meet up', url: url});
      status.textContent = 'Link shared!';
      return;
    } catch (error) {
      if (error.name === 'AbortError') {
        status.textContent = 'Sharing canceled.';
        return;
      }
    }
  }
  try {
    await navigator.clipboard.writeText(url);
    status.textContent = 'Link copied!';
  } catch (error) {
    var link = document.getElementById('share-link');
    link.value = url;
    link.hidden = false;
    link.focus();
    link.select();
    status.textContent = 'Copy this link and send it to your friend.';
  }
});

//this is dumb
function uuidv4() {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
    var r = Math.random() * 16 | 0, v = c == 'x' ? r : (r & 0x3 | 0x8);
    return v.toString(16);
  });
}

function mapLoadFailed() {
  mapError = 'The map could not load. Please reload to try again.';
  searchStatus.textContent = mapError;
}

// Google calls this too when its script loads but authentication fails.
window.gm_authFailure = mapLoadFailed;

async function initMap() {
  if (mapInitializing) return;
  await searchReady;
  if (!savedQuery) return;
  mapInitializing = true;
  if (!navigator.geolocation) {
    mapError = 'Location is unavailable. Use HTTPS and allow location access to search nearby.';
    searchStatus.textContent = mapError;
    return;
  }
  searchStatus.textContent = 'Waiting for your location…';
  navigator.geolocation.getCurrentPosition(async function(position) {
    try {
      var guestCookie = document.cookie.split('; ').find(function(cookie) {
        return cookie.startsWith('GuestId=');
      });
      var guestid = guestCookie ? guestCookie.slice('GuestId='.length) : uuidv4();
      document.cookie = 'GuestId=' + guestid + '; Path=/; SameSite=Lax';
      searchStatus.textContent = 'Loading meetup locations…';
      var response = await fetch('/meetups/' + encodeURIComponent(meetupId), {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({Lat: position.coords.latitude, Long: position.coords.longitude,
          GuestId: guestid, MeetupID: meetupId})
      });
      if (!response.ok) throw new Error('Could not load meetup locations');
      var guests = await response.json();
      map = new google.maps.Map(document.getElementById('map'), {});
      guestPositions = guests.map(function(guest) {
        var location = {lat: parseFloat(guest.Lat), lng: parseFloat(guest.Long)};
        new google.maps.Marker({position: location, map: map, title: 'guest'});
        return location;
      });
      searchNearby();
    } catch (error) {
      mapError = 'Could not load meetup locations. Please reload to try again.';
      searchStatus.textContent = mapError;
    }
  }, function(positionError) {
    mapError = 'Could not get your location: ' + positionError.message +
      '. Allow location access and reload to search nearby.';
    searchStatus.textContent = mapError;
  }, {timeout: 15000});
}

function searchNearby() {
  var generation = ++searchGeneration;
  var query = savedQuery;
  var bounds = new google.maps.LatLngBounds();
  guestPositions.forEach(function(position) { bounds.extend(position); });
  var middle = bounds.getCenter();
  searchStatus.textContent = 'Searching nearby for “' + query + '”…';
  var service = new google.maps.places.PlacesService(map);
  service.nearbySearch({
    location: middle,
    rankBy: google.maps.places.RankBy.DISTANCE,
    keyword: query
  }, function(results, status) {
    // A previous search may finish after the user has submitted another one.
    if (generation !== searchGeneration) return;
    placeMarkers.forEach(function(marker) { marker.setMap(null); });
    placeInfoWindows.forEach(function(infoWindow) { infoWindow.close(); });
    placeMarkers = [];
    placeInfoWindows = [];
    if (status === 'ZERO_RESULTS') {
      map.fitBounds(bounds);
      searchStatus.textContent = 'No nearby places found for “' + query + '”. Try another search.';
      return;
    }
    if (status !== 'OK') {
      searchStatus.textContent = 'Could not search nearby (' + status + '). Please try again.';
      return;
    }
    results.slice(0, 3).forEach(function(place) {
      var marker = new google.maps.Marker({
        map: map,
        icon: {url: place.icon, scaledSize: new google.maps.Size(25, 25)},
        title: place.name,
        animation: google.maps.Animation.DROP,
        position: place.geometry.location
      });
      bounds.extend(place.geometry.location);
      var label = document.createElement('span');
      label.textContent = place.name;
      var infoWindow = new google.maps.InfoWindow({content: label});
      infoWindow.open(map, marker);
      marker.addListener('click', function() { infoWindow.open(map, marker); });
      placeMarkers.push(marker);
      placeInfoWindows.push(infoWindow);
    });
    map.fitBounds(bounds);
    searchStatus.textContent = 'Showing ' + placeMarkers.length + ' nearby places for “' + query + '”.';
  });
}
