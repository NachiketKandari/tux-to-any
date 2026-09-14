using static OaoBackendApi.Common.CommonRequestDTO;
using static OaoBackendApi.OAOApplication.CustomerAuthenticate.DTO.CustOtpDTO;

namespace OaoBackendApi.OAOApplication.CustomerAuthenticate.Service
{
    public interface ICustOtpService
    {
        Task<CustomEventResponse> CustomEvent(CommonMobileRequest request, CancellationToken cancellationToken);
    }
}
