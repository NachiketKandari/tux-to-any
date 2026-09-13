using static OaoBackendApi.Common.CommonRequestDTO;
using static OaoBackendApi.OAOApplication.CustomerAuthenticate.DTO.CustListDTO;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Service
{
    public interface ICustListService
    {
        Task<List<ListEventResponse>> ListEvent(CommonMobileRequest request, CancellationToken cancellationToken);
    }
}
