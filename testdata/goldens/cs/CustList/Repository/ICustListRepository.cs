using System.Data;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository
{
    public interface ICustListRepository
    {
        Task<DataTable> ListEvent(string MobileNo, CancellationToken ct);

    }
}
