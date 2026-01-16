package pl.altkom.asc.lab.micronaut.poc.dashboard.domain;

public interface PolicyRepository {

    boolean indexExists();

    void save(PolicyDocument policyDocument);

    PolicyDocument findByNumber(String number);

    TotalSalesQuery.Result getTotalSales(TotalSalesQuery query);

    SalesTrendsQuery.Result getSalesTrends(SalesTrendsQuery query);

    AgentSalesQuery.Result getAgentSales(AgentSalesQuery query);
}
