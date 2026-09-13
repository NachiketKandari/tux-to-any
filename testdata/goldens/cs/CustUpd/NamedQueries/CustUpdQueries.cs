namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.NamedQueries
{
    public class CustUpdQueries
    {
        public const string UpdateCUSTStageQuery = @"
UPDATE CST_MBL_ACCOPN_RQST SET CST_OLN_STAGE = :vc_cst_stage, CST_OLN_STAGE_DATE = SYSDATE WHERE CST_OLN_MOB = :sql_cst_pan_no AND CST_ACCOPN_SRC = 'E'";
    }
}
