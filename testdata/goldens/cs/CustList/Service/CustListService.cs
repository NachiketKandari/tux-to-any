using OaoBackendApi.Common;
using OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository;
using static OaoBackendApi.OAOApplication.CustomerAuthenticate.DTO.CustListDTO;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Service
{
    public class CustListService : ICustListService
    {
        private readonly ICustListRepository _custList;
        private readonly ILogger<CustListService> _logger;
        public CustListService(ICustListRepository custListRepository, ILogger<CustListService> logger)
        {
            _custList = custListRepository;
            _logger = logger;
        }

        public async Task<List<ListEventResponse>> ListEvent(CommonMobileRequest request, CancellationToken ct)
        {
            try
            {
                var tblRes = await _custList.ListEvent(request.MobileNo, ct);
                var response = new List<ListEventResponse>();
                foreach (DataRow row in tblRes.Rows)
                {
                    response.Add(new ListEventResponse
                    {
                        CST_LEAD_ID = DataReaderHelper.GetStr(row, "CST_LEAD_ID"),
                        CST_FORM_NO = DataReaderHelper.GetStr(row, "CST_FORM_NO"),
                    });
                }
                _logger.LogInformation("ListEvent rows fetched: {Count}", response.Count);
                // tuxgo:TODO residual arm logic (source lines 92-141) — fill here or re-run with the LLM seam enabled
                return response;
            }
            catch (Exception ex) { throw new Exception("Error while executing ListEvent - " + ex.Message); }
        }
    }
}
