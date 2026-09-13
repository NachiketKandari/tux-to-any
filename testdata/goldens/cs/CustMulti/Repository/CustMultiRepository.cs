using OaoBackendApi.Common;
using OaoBackendApi.Common.DbHelper;
using Oracle.ManagedDataAccess.Client;
using System.Data;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository
{
    public class CustMultiRepository : ICustMultiRepository
    {
        private readonly ILogger<CustMultiRepository> _logger;
        private readonly IDbHelper _dbHelperService;

        public CustMultiRepository(ILogger<CustMultiRepository> logger, IDbHelper dbHelperService)
        {
            _logger = logger;
            _dbHelperService = dbHelperService;
        }

        public async Task<DataTable> MultiEvent1(string MobileNo, CancellationToken ct)
        {
            try
            {
                var parameters = new[] { new OracleParameter("sql_cst_pan_no", MobileNo) };
                return await _dbHelperService.ExecuteQueryAsync(CustMultiQueries.GetCUSTDetailsQuery, CommandType.Text, parameters, ct);
            }
            catch (Exception ex) { throw new Exception("GetCUSTDetailsQuery - " + ex.Message); }
        }

        public async Task<int> MultiEvent2(string FormNo, CancellationToken ct)
        {
            try
            {
                var parameters = new[] { new OracleParameter("sql_cst_form_no", FormNo) };
                return await _dbHelperService.ExecuteNonQueryAsync(CustMultiQueries.UpdateCUSTStageQuery, CommandType.Text, parameters, ct);
            }
            catch (Exception ex) { throw new Exception("UpdateCUSTStageQuery - " + ex.Message); }
        }
    }
}
