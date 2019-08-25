using System;
using System.IO;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Mvc;
using Microsoft.Azure.WebJobs;
using Microsoft.Azure.WebJobs.Extensions.Http;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Logging;
using Newtonsoft.Json;

namespace letsmeetup
{
    public static class GuestsAPI
    {
        public class Guest
        {
            public string MeetupId { get; set; } = Guid.NewGuid().ToString("n");
            public string GuestId { get; set; } = Guid.NewGuid().ToString("n");
            public double  Lat { get; set; }
            public double Long { get; set; }
        }

        [FunctionName("GuestsAPI")]
        public static async Task<IActionResult> Run(
            [HttpTrigger(AuthorizationLevel.Anonymous, "get", "post", Route = "meetups/{id}/guests")] 
            HttpRequest req,            
            ILogger log,
            string id)
        {
            log.LogInformation("Getting guests");
            var newguest = new Guest();
            newguest.MeetupId = id;
            if (req.Method == "POST")
            {
                string requestBody = await new StreamReader(req.Body).ReadToEndAsync();
                newguest = JsonConvert.DeserializeObject<Guest>(requestBody);
            }
            return new OkObjectResult(newguest);
        }
    }
}
