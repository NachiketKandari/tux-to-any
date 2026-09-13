using OaoBackendApi.Common;
using OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository;
using static OaoBackendApi.OAOApplication.CustomerAuthenticate.DTO.CustMultiDTO;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Service
{
    public class CustMultiService : ICustMultiService
    {
        private readonly ICustMultiRepository _custMulti;
        private readonly ILogger<CustMultiService> _logger;
        public CustMultiService(ICustMultiRepository custMultiRepository, ILogger<CustMultiService> logger)
        {
            _custMulti = custMultiRepository;
            _logger = logger;
        }

        public async Task<MultiEventResponse> MultiEvent(CommonMobileRequest request, CancellationToken ct)
        {
            try
            {
                var tblRes = await _custMulti.MultiEvent1(request.MobileNo, ct);
                var row = tblRes.Rows.Count > 0 ? tblRes.Rows[0] : null;
                var response = new MultiEventResponse
                {
                    CST_FORM_NO = row != null ? DataReaderHelper.GetStr(row, "CST_FORM_NO") : string.Empty,
                    CST_STAGE = row != null ? DataReaderHelper.GetStr(row, "CST_STAGE") : string.Empty,
                };
                _logger.LogInformation("MultiEvent: CST_FORM_NO={CST_FORM_NO}, CST_STAGE={CST_STAGE}", response.CST_FORM_NO, response.CST_STAGE);
                var affected2 = await _custMulti.MultiEvent2(request.FormNo, ct);
                // tuxgo:TODO residual arm logic (source lines 91-153) — fill here or re-run with the LLM seam enabled
                return response;
            }
            catch (Exception ex) { throw new Exception("Error while executing MultiEvent - " + ex.Message); }
        }
    }
}
