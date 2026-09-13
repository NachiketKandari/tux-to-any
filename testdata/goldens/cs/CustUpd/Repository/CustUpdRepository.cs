using OaoBackendApi.Common;
using OaoBackendApi.Common.DbHelper;
using Oracle.ManagedDataAccess.Client;
using System.Data;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository
{
    public class CustUpdRepository : ICustUpdRepository
    {
        private readonly ILogger<CustUpdRepository> _logger;
        private readonly IDbHelper _dbHelperService;

        public CustUpdRepository(ILogger<CustUpdRepository> logger, IDbHelper dbHelperService)
        {
            _logger = logger;
            _dbHelperService = dbHelperService;
        }

        public async Task<int> UpdateEvent(string Stage, string MobileNo, CancellationToken ct)
        {
            try
            {
                var parameters = new[] { new OracleParameter("vc_cst_stage", Stage), new OracleParameter("sql_cst_pan_no", MobileNo) };
                return await _dbHelperService.ExecuteNonQueryAsync(CustUpdQueries.UpdateCUSTStageQuery, CommandType.Text, parameters, ct);
            }
            catch (Exception ex) { throw new Exception("UpdateCUSTStageQuery - " + ex.Message); }
        }
    }
}
