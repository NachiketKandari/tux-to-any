using OaoBackendApi.Common;
using OaoBackendApi.Common.DbHelper;
using Oracle.ManagedDataAccess.Client;
using System.Data;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository
{
    public class CustOtpRepository : ICustOtpRepository
    {
        private readonly ILogger<CustOtpRepository> _logger;
        private readonly IDbHelper _dbHelperService;

        public CustOtpRepository(ILogger<CustOtpRepository> logger, IDbHelper dbHelperService)
        {
            _logger = logger;
            _dbHelperService = dbHelperService;
        }

        public async Task<DataTable> CustomEvent(string MobileNo, CancellationToken ct)
        {
            try
            {
                var parameters = new[] { new OracleParameter("sql_cst_pan_no", MobileNo) };
                return await _dbHelperService.ExecuteQueryAsync(CustOtpQueries.GetCUSTDetailsQuery, CommandType.Text, parameters, ct);
            }
            catch (Exception ex) { throw new Exception("GetCUSTDetailsQuery - " + ex.Message); }
        }
    }
}
