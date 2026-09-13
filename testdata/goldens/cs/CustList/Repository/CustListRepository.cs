using OaoBackendApi.Common;
using OaoBackendApi.Common.DbHelper;
using Oracle.ManagedDataAccess.Client;
using System.Data;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository
{
    public class CustListRepository : ICustListRepository
    {
        private readonly ILogger<CustListRepository> _logger;
        private readonly IDbHelper _dbHelperService;

        public CustListRepository(ILogger<CustListRepository> logger, IDbHelper dbHelperService)
        {
            _logger = logger;
            _dbHelperService = dbHelperService;
        }

        public async Task<DataTable> ListEvent(string MobileNo, CancellationToken ct)
        {
            try
            {
                var parameters = new[] { new OracleParameter("sql_cst_pan_no", MobileNo) };
                return await _dbHelperService.ExecuteQueryAsync(CustListQueries.GetCLFLEADACCOPNGFEEQuery, CommandType.Text, parameters, ct);
            }
            catch (Exception ex) { throw new Exception("GetCLFLEADACCOPNGFEEQuery - " + ex.Message); }
        }
    }
}
