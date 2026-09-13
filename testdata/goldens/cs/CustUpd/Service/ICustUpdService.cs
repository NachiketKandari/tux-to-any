using static OaoBackendApi.Common.CommonRequestDTO;
using static OaoBackendApi.OAOApplication.CustomerAuthenticate.DTO.CustUpdDTO;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Service
{
    public interface ICustUpdService
    {
        Task<int> UpdateEvent(CommonMobileRequest request, CancellationToken cancellationToken);
    }
}
