using OaoBackendApi.Common;
using OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository;
using static OaoBackendApi.OAOApplication.CustomerAuthenticate.DTO.CustOtpDTO;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Service
{
    public class CustOtpService : ICustOtpService
    {
        private readonly ICustOtpRepository _custOtp;
        private readonly ILogger<CustOtpService> _logger;
        public CustOtpService(ICustOtpRepository custOtpRepository, ILogger<CustOtpService> logger)
        {
            _custOtp = custOtpRepository;
            _logger = logger;
        }

        public async Task<CustomEventResponse> CustomEvent(CommonMobileRequest request, CancellationToken ct)
        {
            try
            {
                var tblRes = await _custOtp.CustomEvent(request.MobileNo, ct);
                var row = tblRes.Rows.Count > 0 ? tblRes.Rows[0] : null;
                var response = new CustomEventResponse
                {
                    CST_FORM_NO = row != null ? DataReaderHelper.GetStr(row, "CST_FORM_NO") : string.Empty,
                    CST_LEAD_ID = row != null ? DataReaderHelper.GetStr(row, "CST_LEAD_ID") : string.Empty,
                    CST_INSERT_DATE = row != null ? DataReaderHelper.GetStr(row, "CST_INSERT_DATE") : string.Empty,
                    CST_STAGE = row != null ? DataReaderHelper.GetStr(row, "CST_STAGE") : string.Empty,
                    CST_FINAL_SOURCE = row != null ? DataReaderHelper.GetStr(row, "CST_FINAL_SOURCE") : string.Empty,
                    CST_EMP_NO = row != null ? DataReaderHelper.GetStr(row, "CST_EMP_NO") : string.Empty,
                };
                _logger.LogInformation("CustomEvent: CST_FORM_NO={CST_FORM_NO}, CST_LEAD_ID={CST_LEAD_ID}, CST_INSERT_DATE={CST_INSERT_DATE}, CST_STAGE={CST_STAGE}, CST_FINAL_SOURCE={CST_FINAL_SOURCE}, CST_EMP_NO={CST_EMP_NO}", response.CST_FORM_NO, response.CST_LEAD_ID, response.CST_INSERT_DATE, response.CST_STAGE, response.CST_FINAL_SOURCE, response.CST_EMP_NO);
                // tuxgo:TODO residual arm logic (source lines 105-211) — fill here or re-run with the LLM seam enabled
                return response;
            }
            catch (Exception ex) { throw new Exception("Error while executing CustomEvent - " + ex.Message); }
        }
    }
}
