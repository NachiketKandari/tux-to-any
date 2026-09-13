using System.Data;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository
{
    public interface ICustUpdRepository
    {
        Task<int> UpdateEvent(string Stage, string MobileNo, CancellationToken ct);

    }
}
