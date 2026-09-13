namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.NamedQueries
{
    public class CustListQueries
    {
        public const string GetCLFLEADACCOPNGFEEQuery = @"
SELECT CST_OBOR_LEAD_ID, CST_FORM_NO FROM CLF_LEAD_ACC_OPNG_FEE WHERE CST_OLN_MOB = :sql_cst_pan_no AND CLF_LEAD_CLOSED(+) = 'N'";
    }
}
