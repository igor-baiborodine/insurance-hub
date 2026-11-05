package pl.altkom.asc.lab.micronaut.poc.gateway.infrastructure;

import io.micronaut.http.HttpRequest;
import io.micronaut.http.HttpResponse;
import io.micronaut.http.HttpStatus;
import io.micronaut.http.MutableHttpResponse;
import io.micronaut.http.exceptions.HttpStatusException;
import io.micronaut.http.filter.ServerFilterChain;
import io.micronaut.security.authentication.AuthorizationException;
import io.reactivex.Flowable;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;

import java.util.Map;

import static org.assertj.core.api.Assertions.assertThat;

class DashboardTotalSalesErrorFilterTest {

    private final DashboardTotalSalesErrorFilter classUnderTest = new DashboardTotalSalesErrorFilter();

    @Nested
    class DoFilter {

        @Test
        void givenDownstreamFailure_thenReturnFixedInternalError() {
            // given
            String privateMarker = "private-downstream-detail";
            ServerFilterChain chain = request -> Flowable.error(new IllegalStateException(privateMarker));

            // when
            MutableHttpResponse<?> result = Flowable.fromPublisher(classUnderTest.doFilter(
                    HttpRequest.POST("/api/dashboard/totalsales", "{}"), chain)).blockingFirst();

            // then
            assertThat(result.code()).isEqualTo(HttpStatus.INTERNAL_SERVER_ERROR.getCode());
            assertThat(result.getBody(Map.class)).hasValueSatisfying(body -> {
                assertThat(body).containsEntry("code", 13).containsEntry("message", "internal error");
                assertThat(body.toString()).doesNotContain(privateMarker);
            });
        }

        @Test
        void givenBindingFailure_thenSanitizeBodyAndPreserveStatus() {
            // given
            ServerFilterChain chain = request -> Flowable.just(
                    HttpResponse.badRequest("private-binding-detail"));

            // when
            MutableHttpResponse<?> result = Flowable.fromPublisher(classUnderTest.doFilter(
                    HttpRequest.POST("/api/dashboard/totalsales", "{}"), chain)).blockingFirst();

            // then
            assertThat(result.code()).isEqualTo(HttpStatus.BAD_REQUEST.getCode());
            assertThat(result.getBody(Map.class)).hasValueSatisfying(body ->
                    assertThat(body).containsEntry("code", 3).containsEntry("message", "invalid request"));
        }

        @Test
        void givenUnauthenticatedRequest_thenPreserveSecurityResponse() {
            // given
            ServerFilterChain chain = request -> Flowable.error(new AuthorizationException(null));

            // when
            MutableHttpResponse<?> result = Flowable.fromPublisher(classUnderTest.doFilter(
                    HttpRequest.POST("/api/dashboard/totalsales", "{}"), chain)).blockingFirst();

            // then
            assertThat(result.code()).isEqualTo(HttpStatus.UNAUTHORIZED.getCode());
            assertThat(result.getBody()).isEmpty();
        }

        @Test
        void givenUnsupportedMediaType_thenPreserveStatusWithSafeBody() {
            // given
            ServerFilterChain chain = request -> Flowable.error(
                    new HttpStatusException(HttpStatus.UNSUPPORTED_MEDIA_TYPE, "private-media-detail"));

            // when
            MutableHttpResponse<?> result = Flowable.fromPublisher(classUnderTest.doFilter(
                    HttpRequest.POST("/api/dashboard/totalsales", "{}"), chain)).blockingFirst();

            // then
            assertThat(result.code()).isEqualTo(HttpStatus.UNSUPPORTED_MEDIA_TYPE.getCode());
            assertThat(result.getBody(Map.class)).hasValueSatisfying(body ->
                    assertThat(body).containsEntry("code", 3)
                            .containsEntry("message", "unsupported media type"));
        }
    }
}
