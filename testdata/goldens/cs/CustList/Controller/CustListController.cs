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
    public class CustListController : ControllerBase
    {
        private readonly ICustListService _custList;
        private readonly ILogger<CustListController> _logger;
        public CustListController(ILogger<CustListController> logger, ICustListService custListService)
        {
            _logger = logger;
            _custList = custListService;
        }

        [HttpPost]
        [Route("list_event_L")]
        public async Task<IActionResult> ListEvent([FromBody] CommonMobileRequest request, CancellationToken cancellationToken)
        {
            try
            {
                var result = await _custList.ListEvent(request, cancellationToken);
                return Ok(ResponseHelper.Success(result));
            }
            catch (Exception ex) { return Ok(ResponseHelper.Error(HttpStatusCode.InternalServerError, ex.Message)); }
        }
    }
}
