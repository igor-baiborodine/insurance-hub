package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.hibernate.HibernateException;
import org.hibernate.engine.spi.SharedSessionContractImplementor;
import org.hibernate.usertype.UserType;
import org.postgresql.util.PGobject;

import java.io.Serializable;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Types;
import java.util.Objects;

public class ProductDefinitionJsonbType implements UserType {

    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    @Override
    public int[] sqlTypes() {
        return new int[] {Types.OTHER};
    }

    @Override
    public Class returnedClass() {
        return ProductDefinition.class;
    }

    @Override
    public boolean equals(Object x, Object y) throws HibernateException {
        return Objects.equals(x, y);
    }

    @Override
    public int hashCode(Object x) throws HibernateException {
        return Objects.hashCode(x);
    }

    @Override
    public Object nullSafeGet(ResultSet resultSet, String[] names, SharedSessionContractImplementor session, Object owner)
            throws HibernateException, SQLException {
        Object value = resultSet.getObject(names[0]);
        if (value == null) {
            return null;
        }

        String json = value instanceof PGobject ? ((PGobject) value).getValue() : value.toString();
        try {
            return OBJECT_MAPPER.readValue(json, ProductDefinition.class);
        } catch (JsonProcessingException e) {
            throw new HibernateException("Cannot deserialize product definition from jsonb", e);
        }
    }

    @Override
    public void nullSafeSet(PreparedStatement statement, Object value, int index, SharedSessionContractImplementor session)
            throws HibernateException, SQLException {
        if (value == null) {
            statement.setNull(index, Types.OTHER);
            return;
        }

        try {
            PGobject jsonObject = new PGobject();
            jsonObject.setType("jsonb");
            jsonObject.setValue(OBJECT_MAPPER.writeValueAsString(value));
            statement.setObject(index, jsonObject);
        } catch (JsonProcessingException e) {
            throw new HibernateException("Cannot serialize product definition to jsonb", e);
        }
    }

    @Override
    public Object deepCopy(Object value) throws HibernateException {
        if (value == null) {
            return null;
        }

        try {
            return OBJECT_MAPPER.readValue(OBJECT_MAPPER.writeValueAsString(value), ProductDefinition.class);
        } catch (JsonProcessingException e) {
            throw new HibernateException("Cannot deep copy product definition", e);
        }
    }

    @Override
    public boolean isMutable() {
        return true;
    }

    @Override
    public Serializable disassemble(Object value) throws HibernateException {
        if (value == null) {
            return null;
        }

        try {
            return OBJECT_MAPPER.writeValueAsString(value);
        } catch (JsonProcessingException e) {
            throw new HibernateException("Cannot disassemble product definition", e);
        }
    }

    @Override
    public Object assemble(Serializable cached, Object owner) throws HibernateException {
        if (cached == null) {
            return null;
        }

        try {
            return OBJECT_MAPPER.readValue(cached.toString(), ProductDefinition.class);
        } catch (JsonProcessingException e) {
            throw new HibernateException("Cannot assemble product definition", e);
        }
    }

    @Override
    public Object replace(Object original, Object target, Object owner) throws HibernateException {
        return deepCopy(original);
    }
}
