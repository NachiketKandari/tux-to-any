using Asp.Versioning;
using Microsoft.AspNetCore.Mvc;
using System.Net;
using OaoBackendApi.Common;
using OaoBackendApi.Helpers;
using OaoBackendApi.OAOApplication.CustomerAuthenticate.Service;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Controller
{
    [ApiController]
    [ApiVersion("1.0")]
    [Route("api/v{version:apiVersion}/[controller]")]
    public class CustUpdController : ControllerBase
    {
        private readonly ICustUpdService _custUpd;
        private readonly ILogger<CustUpdController> _logger;
        public CustUpdController(ILogger<CustUpdController> logger, ICustUpdService custUpdService)
        {
            _logger = logger;
            _custUpd = custUpdService;
        }

        [HttpPost]
        [Route("upd_event_U")]
        public async Task<IActionResult> UpdateEvent([FromBody] CommonMobileRequest request, CancellationToken cancellationToken)
        {
            try
            {
                var result = await _custUpd.UpdateEvent(request, cancellationToken);
                return Ok(ResponseHelper.Success(result));
            }
            catch (Exception ex) { return Ok(ResponseHelper.Error(HttpStatusCode.InternalServerError, ex.Message)); }
        }
    }
}
