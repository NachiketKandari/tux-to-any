using static OaoBackendApi.Common.CommonRequestDTO;
using static OaoBackendApi.OAOApplication.CustomerAuthenticate.DTO.CustMultiDTO;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Service
{
    public interface ICustMultiService
    {
        Task<MultiEventResponse> MultiEvent(CommonMobileRequest request, CancellationToken cancellationToken);
    }
}
