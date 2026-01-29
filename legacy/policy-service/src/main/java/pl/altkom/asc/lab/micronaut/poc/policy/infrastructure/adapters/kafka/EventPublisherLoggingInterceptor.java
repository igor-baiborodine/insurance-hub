package pl.altkom.asc.lab.micronaut.poc.policy.infrastructure.adapters.kafka;

import io.micronaut.aop.InterceptorBean;
import io.micronaut.aop.MethodInterceptor;
import io.micronaut.aop.MethodInvocationContext;
import java.util.Objects;
import javax.inject.Singleton;

import lombok.extern.slf4j.Slf4j;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

@Slf4j
@Singleton
@InterceptorBean(LogEventPublisher.class)
public class EventPublisherLoggingInterceptor implements MethodInterceptor<Object, Object> {

    @Override
    public Object intercept(MethodInvocationContext<Object, Object> context) {
        String methodName = context.getMethodName();
        String policyNumber = Objects.toString(resolvePolicyKey(context), "n/a");
        log.info("Publishing event {} for policy {}", methodName, policyNumber);
        try {
            Object result = context.proceed();
            log.info("Published event {} for policy {}", methodName, policyNumber);
            return result;
        } catch (Exception e) {
            log.error("Publishing event {} for policy {} failed", methodName, policyNumber, e);
            throw e;
        }
    }

    private Object resolvePolicyKey(MethodInvocationContext<Object, Object> context) {
        Object[] values = context.getParameterValues();
        return values.length > 0 ? values[0] : null;
    }
}
