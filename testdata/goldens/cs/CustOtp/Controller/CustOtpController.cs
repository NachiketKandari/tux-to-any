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
    public class CustOtpController : ControllerBase
    {
        private readonly ICustOtpService _custOtp;
        private readonly ILogger<CustOtpController> _logger;
        public CustOtpController(ILogger<CustOtpController> logger, ICustOtpService custOtpService)
        {
            _logger = logger;
            _custOtp = custOtpService;
        }

        [HttpPost]
        [Route("cst_get_dtl_CUSE")]
        public async Task<IActionResult> CustomEvent([FromBody] CommonMobileRequest request, CancellationToken cancellationToken)
        {
            var validation = new CUSEValidator("CUSE").Validate(request);
            if (!validation.IsValid)
            {
                var error = validation.Errors[0].ErrorMessage;
                return Ok(ResponseHelper.Error(HttpStatusCode.BadRequest, error));
            }
            try
            {
                var result = await _custOtp.CustomEvent(request, cancellationToken);
                return Ok(ResponseHelper.Success(result));
            }
            catch (Exception ex) { return Ok(ResponseHelper.Error(HttpStatusCode.InternalServerError, ex.Message)); }
        }
    }
}
