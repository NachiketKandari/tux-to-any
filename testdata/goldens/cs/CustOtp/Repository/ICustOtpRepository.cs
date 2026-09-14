using System.Data;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository
{
    public interface ICustOtpRepository
    {
        Task<DataTable> CustomEvent(string MobileNo, CancellationToken ct);

    }
}
