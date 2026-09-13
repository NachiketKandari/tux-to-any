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
    public class CustMultiController : ControllerBase
    {
        private readonly ICustMultiService _custMulti;
        private readonly ILogger<CustMultiController> _logger;
        public CustMultiController(ILogger<CustMultiController> logger, ICustMultiService custMultiService)
        {
            _logger = logger;
            _custMulti = custMultiService;
        }

        [HttpPost]
        [Route("multi_event_M")]
        public async Task<IActionResult> MultiEvent([FromBody] CommonMobileRequest request, CancellationToken cancellationToken)
        {
            try
            {
                var result = await _custMulti.MultiEvent(request, cancellationToken);
                return Ok(ResponseHelper.Success(result));
            }
            catch (Exception ex) { return Ok(ResponseHelper.Error(HttpStatusCode.InternalServerError, ex.Message)); }
        }
    }
}
