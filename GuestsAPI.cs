using System;
using System.IO;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Mvc;
using Microsoft.Azure.WebJobs;
using Microsoft.Azure.WebJobs.Extensions.Http;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Logging;
using Newtonsoft.Json;
using Microsoft.Azure.Cosmos;

namespace letsmeetup
{
    public class GuestsAPI
    {
        public class Guest
        {
            public string MeetupId { get; set; } = Guid.NewGuid().ToString("n");
            public string GuestId { get; set; } = Guid.NewGuid().ToString("n");
            public double  Lat { get; set; }
            public double Long { get; set; }
        }

        public GuestsAPI(CosmosClient dbClient)
        {
            _dbClient = dbClient;
        }

        private CosmosClient _dbClient;

        [FunctionName("GuestsAPI")]
        public async Task<IActionResult> Run(
            [HttpTrigger(AuthorizationLevel.Anonymous, "get", "post", Route = "meetups/{id}/guests")] 
            HttpRequest req,            
            ILogger log,
            string id)
        {
            log.LogInformation("Getting guests for {id}");
            var newguest = new Guest();
            var guestcontainer = _dbClient.GetContainer("meetupdb", "guests");
            if (req.Method == "POST")
            {
                string requestBody = await new StreamReader(req.Body).ReadToEndAsync();
                newguest = JsonConvert.DeserializeObject<Guest>(requestBody);
                newguest.MeetupId = id;
                log.LogInformation("adding guest {newguest.GuestId}");
                await guestcontainer.CreateItemAsync<Guest>(newguest, new PartitionKey(newguest.MeetupId));
            }
            
            var sqlQueryText = "SELECT * FROM c WHERE c.MeetupId = '{id}'";

            log.LogInformation("Running query: {0}\n", sqlQueryText);

            var results = guestcontainer.GetItemQueryIterator<Guest>(new QueryDefinition(sqlQueryText));
            var guests = await results.ReadNextAsync();            

            return new OkObjectResult(guests);
        }
    }
}
