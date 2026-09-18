namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.NamedQueries
{
    public class CustOtpQueries
    {
        public const string GetCUSTDetailsQuery = @"
SELECT
    NVL(CST_FORM_NO,' '),
    CST_OBOR_LEAD_ID,
    to_char(CST_INSERT_DATE,'dd-Mon-yyyy hh24:mi:ss'),
    CST_OLN_STAGE,
    NVL(CST_FINAL_SOURCE,' '),
    NVL(CST_FINAL_EMP_NO,'NA')
FROM CST_MBL_ACCOPN_RQST, CLD_INFO_CLIENT_DTLS, CLF_LEAD_ACC_OPNG_FEE
WHERE CST_OLN_MOB = :sql_cst_pan_no
    AND CST_PAN_NO = CLD_PAN_NO(+)
    AND CST_STATUS IN ('Z','P')
    AND CST_ACCOPN_SRC = 'E'
    AND CLF_LEAD_CLOSED(+) = 'N'";
    }
}
