namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.NamedQueries
{
    public class CustMultiQueries
    {
        public const string GetCUSTDetailsQuery = @"
SELECT
    CST_FORM_NO,
    CST_OLN_STAGE
FROM CST_MBL_ACCOPN_RQST
WHERE CST_OLN_MOB = :sql_cst_pan_no
    AND CST_ACCOPN_SRC = 'E'";
        public const string UpdateCUSTStageQuery = @"
UPDATE CST_MBL_ACCOPN_RQST
SET CST_OLN_STAGE = 'Z'
WHERE CST_FORM_NO = :sql_cst_form_no";
    }
}
