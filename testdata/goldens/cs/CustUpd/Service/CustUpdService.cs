using OaoBackendApi.Common;
using OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository;
using static OaoBackendApi.OAOApplication.CustomerAuthenticate.DTO.CustUpdDTO;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Service
{
    public class CustUpdService : ICustUpdService
    {
        private readonly ICustUpdRepository _custUpd;
        private readonly ILogger<CustUpdService> _logger;
        public CustUpdService(ICustUpdRepository custUpdRepository, ILogger<CustUpdService> logger)
        {
            _custUpd = custUpdRepository;
            _logger = logger;
        }

        public async Task<int> UpdateEvent(CommonMobileRequest request, CancellationToken ct)
        {
            try
            {
                var affected = await _custUpd.UpdateEvent(request.Stage, request.MobileNo, ct);
                // tuxgo:TODO residual arm logic (source lines 89-140) — fill here or re-run with the LLM seam enabled
                return affected;
            }
            catch (Exception ex) { throw new Exception("Error while executing UpdateEvent - " + ex.Message); }
        }
    }
}
