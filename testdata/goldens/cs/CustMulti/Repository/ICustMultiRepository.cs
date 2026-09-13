using System.Data;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Repository
{
    public interface ICustMultiRepository
    {
        Task<DataTable> MultiEvent1(string MobileNo, CancellationToken ct);

        Task<int> MultiEvent2(string FormNo, CancellationToken ct);

    }
}
