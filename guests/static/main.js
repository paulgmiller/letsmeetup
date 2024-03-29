var map;
//this is dumb
function uuidv4() {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
    var r = Math.random() * 16 | 0, v = c == 'x' ? r : (r & 0x3 | 0x8);
    return v.toString(16);
  });
}

function initMap() {
  let urlParams = new URLSearchParams(window.location.search);
  let meetup = urlParams.get('meetup');
  if (meetup == null)
  {
      meetup = urlParams.get('m')
  }
  if (meetup == null)
  {
    meetup  = btoa(Math.floor(Math.random() * Math.floor(1000000)))
    window.location.search += ('m=' + meetup)
  }
  console.info("getting current postition")
  navigator.geolocation.getCurrentPosition(function(position) {
      var guestid = Cookies.get('GuestId'); 
      if(guestid === undefined)
      {           
        guestid = uuidv4()
        Cookies.set('GuestId', guestid) //don't think this is working
      }
      var me =  JSON.stringify({Lat: position.coords.latitude, Long: position.coords.longitude, GuestId: guestid, MeetupID: meetup});
      console.info("fetching /meetups/"+meetup)
      promise = fetch("/meetups/"+meetup, {method: 'POST', body: me, headers: {'Content-Type': 'application/json'}})
      promise.then(function(response) {
          
          if (!response.ok) {
            alert("guests api: "+ response.statuText)
            return; 
          }
          console.info("processing json")
          response.json().then(function(guests) { 
            console.info(JSON.stringify(guests))
            var guest;
            var bounds = new google.maps.LatLngBounds();
            map = new google.maps.Map(document.getElementById('map'), {});
            for (guest of guests)
            {
              console.info(JSON.stringify(guest))
              var guestpos = { lat: parseFloat(guest.Lat),  lng: parseFloat(guest.Long)}
              var guestmarker = new google.maps.Marker({position: guestpos, map: map, title: "guest"});
              bounds.extend(guestpos)
            }
          
          //var paul = { lat: 47.592610, lng: -122.158050 }
          var middle = bounds.getCenter()
          var service = new google.maps.places.PlacesService(map);
          
          let query = urlParams.get('query');
          if( query == null)
          {
            query = 'food'
          }
          search_request = {location: middle, rankBy: google.maps.places.RankBy.DISTANCE, keyword: query}
          console.info(JSON.stringify(search_request))
          service.nearbySearch(
            //search in bounds? add a query from query parametr
            search_request,
            function(results, status, pagination) {
              if (status == "ZERO_RESULTS")
              {
                map.fitBounds(bounds)
                var marker = new google.maps.Marker({
                  map: map,
                  animation: google.maps.Animation.DROP,
                  position: middle
                });
                map.fitBounds(bounds)
                var infowindow = new google.maps.InfoWindow({
                  content: "NOTHING IS HERE"
                });
                infowindow.open(map, marker);
                return;
              }
              else if (status !== 'OK') {
                alert('nearyby search ' + status)
                console.log(results)
                return; 
              }
              for (var i = 0; i < results.length && i < 3; i++) {
                var place = results[i]
                //this just makes a knife and fork
                var image = {
                  url: place.icon,
                  size: new google.maps.Size(71, 71),
                  origin: new google.maps.Point(0, 0),
                  anchor: new google.maps.Point(17, 34),
                  scaledSize: new google.maps.Size(25, 25)
                };
                //marker isn't clickable
                var marker = new google.maps.Marker({
                  map: map,
                  icon: image,
                  title: place.name,
                  animation: google.maps.Animation.DROP,
                  position: place.geometry.location
                });
                bounds.extend(place.geometry.location)
                map.fitBounds(bounds)
                var infowindow = new google.maps.InfoWindow({
                  content: place.name
                });
                infowindow.open(map, marker);
                marker.addListener('click', function() { infowindow.open(map, marker) });
              }
            });
          });
        })
  }, function(positionError) {
    alert("position error: " + positionError.message)
  });
}
